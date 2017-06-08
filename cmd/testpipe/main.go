package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"

	yaml "gopkg.in/yaml.v2"

	flags "github.com/jessevdk/go-flags"
	"github.com/krishicks/testpipe"
)

type opts struct {
	PipelinePath []FileFlag `long:"pipeline" short:"p" value-name:"PATH" description:"Path to pipeline"`
	ConfigPath   FileFlag   `long:"config" short:"c" value-name:"PATH" description:"Path to config"`
}

func main() {
	var o opts
	_, err := flags.Parse(&o)
	if err != nil {
		log.Fatalf("error: %s\n", err)
	}

	var config testpipe.Config
	if o.ConfigPath.Path() != "" {
		bs, err := ioutil.ReadFile(o.ConfigPath.Path())
		if err != nil {
			log.Fatalf("Failed reading config file: %s", err)
		}
		err = yaml.Unmarshal(bs, &config)
		if err != nil {
			log.Fatalf("Failed unmarshaling config file: %s", err)
		}
	}

	for i := range o.PipelinePath {
		t := testpipe.New(o.PipelinePath[i].Path(), config)
		err = t.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err.Error())
			os.Exit(1)
		}
	}
}
