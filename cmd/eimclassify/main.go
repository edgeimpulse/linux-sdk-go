// Command eimclassify launches a classification model process, reads features
// from files named on the command line, and classifies each set of features,
// printing the results.
//
// Example:
//
// 	eimclassify ../../models/linux-x86/continuous-gestures.eim ../../node/examples/features.txt
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
)

var (
	traceDir string
)

func init() {
	flag.StringVar(&traceDir, "tracedir", "", "if set, store the parsed classify data to the named directory")
}

func usage() {
	log.Println("usage: eimclassify model featurefile ...")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		usage()
	}

	ropts := &edgeimpulse.RunnerOpts{
		TraceDir: traceDir,
	}
	runner, err := edgeimpulse.NewRunnerProcess(args[0], ropts)
	if err != nil {
		log.Fatalf("new runner: %v", err)
	}

	log.Printf("project %s\nmodel %s", runner.Project(), runner.ModelParameters())

	fatalf := func(format string, args ...interface{}) {
		log.Printf(format, args...)
		runner.Close()
		os.Exit(1)
	}

	files := args[1:]
	datas := make([][]float64, len(files))
	for i, f := range files {
		var err error
		datas[i], err = readFile(f)
		if err != nil {
			fatalf("reading file: %v", err)
		}
	}

	for _, data := range datas {
		data := data
		resp, err := runner.Classify(data)
		if err != nil {
			log.Printf("classify: %v", err)
		} else {
			fmt.Printf("%s\n", resp)
		}
	}
	runner.Close()
}

func readFile(path string) ([]float64, error) {
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data := []float64{}
	for _, e := range strings.Split(string(buf), ",") {
		e = strings.TrimSpace(e)
		v, err := strconv.ParseFloat(e, 64)
		if err != nil {
			i, err := strconv.ParseInt(e, 0, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing: %v", err)
			}
			v = float64(i)
		}
		data = append(data, v)
	}
	return data, nil
}
