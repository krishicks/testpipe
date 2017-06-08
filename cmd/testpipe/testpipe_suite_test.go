package main_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"testing"
)

func TestTestpipe(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Testpipe Suite")
}

var cmdPath string

var _ = SynchronizedBeforeSuite(func() []byte {
	compiledPath, err := gexec.Build("github.com/krishicks/testpipe/cmd/testpipe")
	Expect(err).NotTo(HaveOccurred())

	return []byte(compiledPath)
}, func(data []byte) {
	cmdPath = string(data)
})

var _ = SynchronizedAfterSuite(func() {
}, func() {
	gexec.CleanupBuildArtifacts()
})
