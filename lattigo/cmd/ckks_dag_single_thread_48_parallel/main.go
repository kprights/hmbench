package main

import (
	"fmt"
	"os"

	"lattigo/dagbench"
)

func main() {
	config := dagbench.NewWideDAGConfig(48, false)
	if err := dagbench.RunWideDAGExample(config); err != nil {
		fmt.Fprintf(os.Stderr, "ckks_dag_single_thread_48_parallel failed: %v\n", err)
		os.Exit(1)
	}
}
