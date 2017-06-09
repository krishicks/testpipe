package testpipe

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/concourse/atc"
	yaml "gopkg.in/yaml.v2"
)

type Config struct {
	ResourceMap map[string]string `yaml:"resource_map"`
}

type TestPipe struct {
	path   string
	config Config
}

type ParamsData struct {
	PipelinePath string
	JobName      string
	TaskName     string
	Extras       []string
	Missing      []string
}

type ResourcesData struct {
	PipelinePath string
	JobName      string
	TaskName     string
	Missing      []string
}

func New(path string, config Config) *TestPipe {
	return &TestPipe{
		path:   path,
		config: config,
	}
}

var placeholderRegexp = regexp.MustCompile("{{([a-zA-Z0-9-_]+)}}")

func (t *TestPipe) Run() error {
	configBytes, err := ioutil.ReadFile(t.path)
	if err != nil {
		return err
	}

	cleanConfigBytes := placeholderRegexp.ReplaceAll(configBytes, []byte("true"))

	var config atc.Config
	err = yaml.Unmarshal(cleanConfigBytes, &config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal pipeline at %s: %s", t.path, err)
	}

	for _, job := range config.Jobs {
		var resources []string
		var tasks []atc.PlanConfig

		for _, planConfig := range flattenedPlan(&job.Plan) {
			switch {
			case planConfig.Get != "":
				resources = append(resources, planConfig.Get)

				if planConfig.Resource != "" {
					resources = append(resources, planConfig.Resource)
					origPath := t.config.ResourceMap[planConfig.Resource]
					t.config.ResourceMap[planConfig.Get] = origPath
				}

			case planConfig.Put != "":
				resources = append(resources, planConfig.Put)

			case planConfig.Task != "":
				canonicalTask := &planConfig

				if planConfig.TaskConfigPath != "" {
					if len(t.config.ResourceMap) == 0 {
						return fmt.Errorf("failed to load %s; no config provided", planConfig.TaskConfigPath)
					}

					var path string
					path, err = taskPath(resources, planConfig.TaskConfigPath, t.config.ResourceMap)
					if err != nil {
						return err
					}

					canonicalTask, err = t.loadTaskFromPath(path, &planConfig)
					if err != nil {
						return err
					}
				}

				if canonicalTask.TaskConfig.Outputs != nil {
					for i := range canonicalTask.TaskConfig.Outputs {
						resources = append(resources, canonicalTask.TaskConfig.Outputs[i].Name)
					}
				}

				err = t.testParityOfParams(canonicalTask, job.Name)
				if err != nil {
					return err
				}

				if len(canonicalTask.TaskConfig.Inputs) > 0 {
					err = t.testPresenceOfRequiredResources(resources, canonicalTask, job.Name)
					if err != nil {
						return err
					}
				}

				tasks = append(tasks, *canonicalTask)
			}
		}
	}

	return nil
}

func taskPath(
	resources []string,
	taskConfigPath string,
	resourceMap map[string]string,
) (string, error) {
	resourceRoot := strings.Split(taskConfigPath, string(os.PathSeparator))[0]

	if resourcePath, ok := resourceMap[resourceRoot]; ok && resourcePath != "" {
		path := filepath.Join(resourcePath, strings.Replace(taskConfigPath, resourceRoot, "", -1))
		return path, nil
	}

	return "", fmt.Errorf("failed to find path for task: %s", taskConfigPath)
}

func (t *TestPipe) loadTaskFromPath(path string, task *atc.PlanConfig) (*atc.PlanConfig, error) {
	bs, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open task at %s", path)
	}

	var taskConfig atc.TaskConfig
	err = yaml.Unmarshal(bs, &taskConfig)
	if err != nil {
		return nil, err
	}

	newTask := *task
	newTask.TaskConfig = &taskConfig

	return &newTask, nil
}

func (t *TestPipe) testPresenceOfRequiredResources(
	resources []string,
	task *atc.PlanConfig,
	jobName string,
) error {
	var missing []string
OUTER:
	for _, input := range task.TaskConfig.Inputs {
		for _, resource := range resources {
			if input.Name == resource {
				continue OUTER
			}

			if v, ok := task.InputMapping[input.Name]; ok && v == resource {
				continue OUTER
			}
		}

		missing = append(missing, input.Name)
	}

	if len(missing) > 0 {
		paramsTemplate := `
  Pipeline:	{{.PipelinePath}}
  Job:		{{.JobName}}
  Task:		{{.TaskName}}
  {{if .Missing }}
  Missing resources:
    {{- range .Missing}}
    {{.}}
    {{- end}}
  {{end -}}
`

		tmpl := template.Must(template.New("resources").Parse(paramsTemplate))
		buf := &bytes.Buffer{}
		data := ResourcesData{
			PipelinePath: t.path,
			JobName:      jobName,
			TaskName:     task.Name(),
			Missing:      missing,
		}
		if err := tmpl.Execute(buf, data); err != nil {
			log.Fatalf("failed to execute template: %s", err)
		}

		return fmt.Errorf("Task invocation is missing resources: %s", buf.String())
	}

	return nil
}

func (t *TestPipe) testParityOfParams(
	task *atc.PlanConfig,
	jobName string,
) error {
	var extras, missing []string

	for k := range task.TaskConfig.Params {
		if _, ok := task.Params[k]; !ok {
			missing = append(missing, k)
		}
	}

	for k := range task.Params {
		if _, ok := task.TaskConfig.Params[k]; !ok {
			extras = append(extras, k)
		}
	}

	if len(missing) > 0 || len(extras) > 0 {
		paramsTemplate := `
  Pipeline:	{{.PipelinePath}}
  Job:		{{.JobName}}
  Task:		{{.TaskName}}
  {{if .Extras}}
  Extra fields that should be removed:
    {{- range .Extras}}
    {{.}}
    {{- end}}
  {{end -}}

  {{- if .Missing }}
  Missing fields that should be added:
    {{- range .Missing}}
    {{.}}
    {{- end}}
  {{end -}}
`

		tmpl := template.Must(template.New("params").Parse(paramsTemplate))
		buf := &bytes.Buffer{}
		data := ParamsData{
			PipelinePath: t.path,
			JobName:      jobName,
			TaskName:     task.Name(),
			Extras:       extras,
			Missing:      missing,
		}
		if err := tmpl.Execute(buf, data); err != nil {
			log.Fatalf("failed to execute template: %s", err)
		}

		return fmt.Errorf("Params do not have parity: %s", buf.String())
	}

	return nil
}

func flattenedPlan(seq *atc.PlanSequence) []atc.PlanConfig {
	var flatPlan []atc.PlanConfig

	for _, planConfig := range *seq {
		if planConfig.Aggregate != nil {
			flatPlan = append(flatPlan, flattenedPlan(planConfig.Aggregate)...)
		}

		if planConfig.Do != nil {
			flatPlan = append(flatPlan, flattenedPlan(planConfig.Do)...)
		}

		if planConfig.Get != "" || planConfig.Put != "" || planConfig.Task != "" {
			flatPlan = append(flatPlan, planConfig)
		}
	}

	return flatPlan
}
