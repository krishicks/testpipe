package main_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Main", func() {
	var (
		pipelinePath string

		configFilePath string
		tmpDir         string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = ioutil.TempDir("", "testpipe")
		Expect(err).NotTo(HaveOccurred())

		resourcesDir, err := ioutil.TempDir(tmpDir, "resources")
		Expect(err).NotTo(HaveOccurred())

		someResourceDir := filepath.Join(resourcesDir, "some-resource")

		testpipeConfig := fmt.Sprintf(`---
resource_map:
  some-resource: %s`, someResourceDir)

		testpipeConfigFile, err := ioutil.TempFile(tmpDir, "testpipe-config.yml")
		Expect(err).NotTo(HaveOccurred())
		defer testpipeConfigFile.Close()

		configFilePath = testpipeConfigFile.Name()

		_, err = io.Copy(testpipeConfigFile, strings.NewReader(testpipeConfig))
		Expect(err).NotTo(HaveOccurred())

		taskPath := filepath.Join(someResourceDir, "task.yml")

		err = os.MkdirAll(filepath.Dir(taskPath), os.ModePerm)
		Expect(err).NotTo(HaveOccurred())

		taskConfig := `---
params:
  some_param:
`

		err = ioutil.WriteFile(taskPath, []byte(taskConfig), os.ModePerm)
		Expect(err).NotTo(HaveOccurred())

		pipelineFile, err := ioutil.TempFile(tmpDir, "pipeline.yml")
		Expect(err).NotTo(HaveOccurred())
		defer pipelineFile.Close()

		pipelinePath = pipelineFile.Name()

		pipelineConfig := `---
jobs:
- name: some-job
  plan:
  - get: some-resource
  - task: some-task
    file: some-resource/task.yml
    params:
      some_param: some-value
`

		_, err = io.Copy(pipelineFile, strings.NewReader(pipelineConfig))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("exits successfully", func() {
		cmd := exec.Command(cmdPath, "-p", pipelinePath, "-c", configFilePath)
		session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred())

		Eventually(session).Should(gexec.Exit(0))
	})

	Context("when the pipeline specifies params that a task does not require", func() {
		BeforeEach(func() {
			pipelineConfig := fmt.Sprintf(`---
jobs:
- name: some-job
  plan:
  - task: some-task
    file: some-resource/task.yml
    params:
      some_param: A
      some_other_param: B
`)

			err := ioutil.WriteFile(pipelinePath, []byte(pipelineConfig), os.ModePerm)
			Expect(err).NotTo(HaveOccurred())
		})

		It("exits with error having printed errors", func() {
			cmd := exec.Command(cmdPath, "-p", pipelinePath, "-c", configFilePath)
			session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session.Err).Should(gbytes.Say("Extra fields that should be removed"))
			Eventually(session.Err).Should(gbytes.Say("some_other_param"))

			Eventually(session).Should(gexec.Exit(1))
		})
	})

	Context("when the pipeline does not specify params that a task requires", func() {
		BeforeEach(func() {
			pipelineConfig := fmt.Sprintf(`---
jobs:
- name: some-job
  plan:
  - task: some-task
    file: some-resource/task.yml
    params: {}
`)

			err := ioutil.WriteFile(pipelinePath, []byte(pipelineConfig), os.ModePerm)
			Expect(err).NotTo(HaveOccurred())
		})

		It("exits with error having printed errors", func() {
			cmd := exec.Command(cmdPath, "-p", pipelinePath, "-c", configFilePath)
			session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session.Err).Should(gbytes.Say("Missing fields that should be added"))
			Eventually(session.Err).Should(gbytes.Say("some_param"))

			Eventually(session).Should(gexec.Exit(1))
		})
	})
})
