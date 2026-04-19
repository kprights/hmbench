package main

import (
	"fmt"
	"os"

	"lattigo/dagbench"
)

func main() {
	config := dagbench.NewWideDAGConfig(48, true)
	if err := dagbench.RunWideDAGExample(config); err != nil {
		fmt.Fprintf(os.Stderr, "ckks_dag_manual_parallel_48 failed: %v\n", err)
		os.Exit(1)
	}
}
