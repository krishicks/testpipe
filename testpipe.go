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
	tmpl   *template.Template
}

type TemplateData struct {
	Type         string
	PipelinePath string
	JobName      string
	TaskName     string
	Extras       []string
	Missing      []string
}

const outputTemplate = `
  Pipeline:	{{.PipelinePath}}
  Job:		{{.JobName}}
  Task:		{{.TaskName}}
  {{if .Extras}}
  Extra {{.Type}} that should be removed:
    {{- range .Extras}}
    {{.}}
    {{- end}}
  {{end -}}
  {{if .Missing }}
  Missing {{.Type}} that should be added:
    {{- range .Missing}}
    {{.}}
    {{- end}}
  {{end -}}
`

func New(path string, config Config) *TestPipe {
	return &TestPipe{
		path:   path,
		config: config,
		tmpl:   template.Must(template.New("output").Parse(outputTemplate)),
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
				canonicalTask, err := flattenTask(resourceMap, &planConfig, job.Name)
				if err != nil {
					return err
				}

				err = testParityOfParams(canonicalTask, job.Name, t.path, t.tmpl)
				if err != nil {
					return err
				}

				err = testPresenceOfRequiredResources(resources, canonicalTask, job.Name, t.path, t.tmpl)
				if err != nil {
					return err
				}

				tasks = append(tasks, *canonicalTask)

				if canonicalTask.TaskConfig.Outputs != nil {
					for i := range canonicalTask.TaskConfig.Outputs {
						resources = append(resources, canonicalTask.TaskConfig.Outputs[i].Name)
					}
				}

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
	tmpl *template.Template,
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
		buf := &bytes.Buffer{}
		data := TemplateData{
			Type:         "resources",
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
	tmpl *template.Template,
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
		buf := &bytes.Buffer{}
		data := TemplateData{
			Type:         "params",
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
		switch {
		case planConfig.Aggregate != nil:
			flatPlan = append(flatPlan, flattenedPlan(planConfig.Aggregate)...)

		case planConfig.Do != nil:
			flatPlan = append(flatPlan, flattenedPlan(planConfig.Do)...)

		case planConfig.Get != "", planConfig.Put != "", planConfig.Task != "":
			flatPlan = append(flatPlan, planConfig)
		}
	}

	return flatPlan
}

func flattenTask(
	resourceMap map[string]string,
	task *atc.PlanConfig,
	jobName string,
) (*atc.PlanConfig, error) {
	result := task

	if task.TaskConfigPath != "" {
		var err error
		result, err = loadTask(resourceMap, task)
		if err != nil {
			return nil, err
		}
	}

	if result.TaskConfig == nil {
		return nil, fmt.Errorf("task %s/%s is missing a definition", jobName, task.Name())
	}

	if result.TaskConfig.Run.Path == "" {
		return nil, fmt.Errorf("task %s/%s is missing a path", jobName, task.Name())
	}

	return result, nil
}

func loadTask(
	resourceMap map[string]string,
	task *atc.PlanConfig,
) (*atc.PlanConfig, error) {
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

	result := task
	result.TaskConfig = &taskConfig

	return result, nil
}
