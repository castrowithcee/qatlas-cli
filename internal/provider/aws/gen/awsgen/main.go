// awsgen writes the generated AWS tools of the provider package from the model extracts and the curation
// tables. go generate runs it in internal/provider/aws:
//
//	go generate ./internal/provider/aws/...
//
// A model update is a separate, deliberate step: set the commit, retrieved_on and upstream hashes in
// gen/model/pin.json, download the upstream files into a directory laid out like api-models-aws, and run
//
//	go run ./gen/awsgen extract <directory of the upstream files>
//
// from internal/provider/aws. It checks every upstream hash, writes the extracts, and records their hashes.
// Then run go generate. Neither step runs in tests.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/gen"
)

func main() {
	modelDir := filepath.Join("gen", gen.ModelDir)
	var err error
	switch {
	case len(os.Args) == 1:
		err = gen.Generate(modelDir, ".")
	case len(os.Args) == 3 && os.Args[1] == "extract":
		err = gen.UpdateExtracts(modelDir, os.Args[2])
	default:
		err = fmt.Errorf("usage: awsgen | awsgen extract <directory of the upstream files>")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "awsgen:", err)
		os.Exit(1)
	}
}
