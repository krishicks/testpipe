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
		for _, task := range allTasksInPlan(&job.Plan) {
			root := strings.Split(task.TaskConfigPath, string(os.PathSeparator))[0]
			if resourcePath, ok := t.config.ResourceMap[root]; ok {
				err = t.testParityOfParams(job.Name, task.Name(), task.Params, task.TaskConfigPath, resourcePath)
				if err != nil {
					return err
				}
			} else {
				//TODO: Support renamed resources
				// return fmt.Errorf("path on disk to %s not found in config", task.TaskConfigPath)
			}
		}
	}

	return nil
}

func (t *TestPipe) testParityOfParams(
	jobName string,
	taskName string,
	taskParams atc.Params,
	taskConfigPath string,
	resourcePath string,
) error {
	bs, err := ioutil.ReadFile(filepath.Join(filepath.Dir(resourcePath), taskConfigPath))
	if err != nil {
		return err
	}

	taskConfig := atc.TaskConfig{}
	err = yaml.Unmarshal(bs, &taskConfig)
	if err != nil {
		return err
	}

	var extras, missing []string

	for k := range taskConfig.Params {
		if _, ok := taskParams[k]; !ok {
			missing = append(missing, k)
		}
	}

	for k := range taskParams {
		if _, ok := taskConfig.Params[k]; !ok {
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
