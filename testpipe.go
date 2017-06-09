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
		return err
	}

	for _, job := range config.Jobs {
		tasks := allTasksInPlan(&job.Plan)
		for _, task := range tasks {
			canonicalTask, err := t.canonicalTask(&task)
			if err != nil {
				return err
			}

			err = t.testParityOfParams(canonicalTask, job.Name, task.Name())
			if err != nil {
				return err
			}

			resources := availableResources(&job.Plan)
			err = t.testPresenceOfRequiredResources(resources, canonicalTask, job.Name)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (t *TestPipe) canonicalTask(task *atc.PlanConfig) (*atc.PlanConfig, error) {
	if task.TaskConfigPath == "" {
		return task, nil
	}

	newTask := *task

	resourceRoot := strings.Split(task.TaskConfigPath, string(os.PathSeparator))[0]

	if resourcePath, ok := t.config.ResourceMap[resourceRoot]; ok {
		bs, err := ioutil.ReadFile(filepath.Join(filepath.Dir(resourcePath), task.TaskConfigPath))
		if err != nil {
			return nil, err
		}

		var taskConfig atc.TaskConfig
		err = yaml.Unmarshal(bs, &taskConfig)
		if err != nil {
			return nil, err
		}

		newTask.TaskConfig = &taskConfig
	}

	return &newTask, nil
}

func (t *TestPipe) testPresenceOfRequiredResources(
	resources []string,
	task *atc.PlanConfig,
	jobName string,
) error {
	if len(task.TaskConfig.Inputs) == 0 {
		return nil
	}

	var missing []string
OUTER:
	for _, input := range task.TaskConfig.Inputs {
		for _, actual := range resources {
			if input.Name == actual {
				continue OUTER
			}

			for k, v := range task.InputMapping {
				if k == input.Name && v == actual {
					continue OUTER
				}
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
	taskName string,
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
			TaskName:     taskName,
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

func allTasksInPlan(seq *atc.PlanSequence) []atc.PlanConfig {
	var tasks []atc.PlanConfig

	for _, planConfig := range *seq {
		switch {
		case planConfig.Aggregate != nil:
			tasks = append(tasks, allTasksInPlan(planConfig.Aggregate)...)
		case planConfig.Do != nil:
			tasks = append(tasks, allTasksInPlan(planConfig.Do)...)
		case planConfig.Task != "":
			tasks = append(tasks, planConfig)
		case planConfig.Get != "", planConfig.Put != "":
		default:
			log.Fatalf("unknown item in plan: %#v", planConfig)
		}
	}

	return tasks
}

func availableResources(seq *atc.PlanSequence) []string {
	var resources []string

	for _, planConfig := range *seq {
		if planConfig.Aggregate != nil {
			resources = append(resources, availableResources(planConfig.Aggregate)...)
		}

		if planConfig.Do != nil {
			resources = append(resources, availableResources(planConfig.Do)...)
		}

		if planConfig.Get != "" {
			resources = append(resources, planConfig.Get)
		}

		if planConfig.Put != "" {
			resources = append(resources, planConfig.Put)
		}
	}

	return resources
}
