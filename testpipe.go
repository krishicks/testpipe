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

		resourceMap := make(map[string]string, len(t.config.ResourceMap))
		for k, v := range t.config.ResourceMap {
			resourceMap[k] = v
		}

		for _, planConfig := range flattenedPlan(&job.Plan) {
			switch {
			case planConfig.Get != "":
				resources = append(resources, planConfig.Get)

				if planConfig.Resource != "" {
					resources = append(resources, planConfig.Resource)
					origPath := resourceMap[planConfig.Resource]
					resourceMap[planConfig.Get] = origPath
				}

			case planConfig.Put != "":
				resources = append(resources, planConfig.Put)

			case planConfig.Task != "":
				canonicalTask, err := flattenTask(resourceMap, &planConfig)
				if err != nil {
					return err
				}

				if canonicalTask.TaskConfig == nil {
					return fmt.Errorf("%s/%s is missing a definition", job.Name, canonicalTask.Name())
				}

				if canonicalTask.TaskConfig.Outputs != nil {
					for i := range canonicalTask.TaskConfig.Outputs {
						resources = append(resources, canonicalTask.TaskConfig.Outputs[i].Name)
					}
				}

				err = testParityOfParams(canonicalTask, job.Name, t.path)
				if err != nil {
					return err
				}

				if len(canonicalTask.TaskConfig.Inputs) > 0 {
					err = testPresenceOfRequiredResources(resources, canonicalTask, job.Name, t.path)
					if err != nil {
						return err
					}
				}

				tasks = append(tasks, *canonicalTask)

				for _, v := range canonicalTask.OutputMapping {
					resources = append(resources, v)
				}
			}
		}
	}

	return nil
}

func testPresenceOfRequiredResources(
	resources []string,
	task *atc.PlanConfig,
	jobName string,
	pipelinePath string,
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
			PipelinePath: pipelinePath,
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

func testParityOfParams(
	task *atc.PlanConfig,
	jobName string,
	pipelinePath string,
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
			PipelinePath: pipelinePath,
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

func flattenTask(
	resourceMap map[string]string,
	task *atc.PlanConfig,
) (*atc.PlanConfig, error) {
	if task.TaskConfigPath == "" {
		return task, nil
	}

	if len(resourceMap) == 0 {
		return nil, fmt.Errorf("failed to load %s; no config provided", task.TaskConfigPath)
	}

	resourceRoot := strings.Split(task.TaskConfigPath, string(os.PathSeparator))[0]

	var path string
	if resourcePath, ok := resourceMap[resourceRoot]; ok && resourcePath != "" {
		path = filepath.Join(resourcePath, strings.Replace(task.TaskConfigPath, resourceRoot, "", -1))
	} else {
		return nil, fmt.Errorf("failed to find path for task: %s", task.TaskConfigPath)
	}

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
