package main

import (
	"fmt"
	"os"
	"time"

	"lattigo/dagbench"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ckks_dag_manual_parallel failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	exampleStart := time.Now()
	gomaxprocs := dagbench.ConfigureGoMaxProcs(dagbench.DefaultParallelism)

	var timings dagbench.Timings

	setupStart := time.Now()
	params, err := dagbench.NewParameters()
	if err != nil {
		return fmt.Errorf("create CKKS parameters: %w", err)
	}
	timings.Setup = time.Since(setupStart)

	ctx, ctxTimings, err := dagbench.NewHEContext(params)
	if err != nil {
		return fmt.Errorf("create HE context: %w", err)
	}
	timings.Keygen = ctxTimings.Keygen
	timings.RuntimeSetup = ctxTimings.RuntimeSetup

	messageStart := time.Now()
	messages := dagbench.GenerateMessages(params, dagbench.DefaultSeed)
	timings.MessagePrep = time.Since(messageStart)

	referenceStart := time.Now()
	expected := dagbench.BuildReference(messages)
	timings.Reference = time.Since(referenceStart)

	inputs, ioTimings, err := dagbench.EncodeEncrypt(ctx, messages)
	if err != nil {
		return err
	}
	timings.Encode = ioTimings.Encode
	timings.Encrypt = ioTimings.Encrypt

	var dagTrace []dagbench.DagTraceItem
	evaluationStart := time.Now()
	resultCipher, err := dagbench.ManualParallelWorkloadWithTrace(ctx, inputs, &dagTrace)
	if err != nil {
		return fmt.Errorf("evaluate manual-parallel DAG: %w", err)
	}
	timings.Evaluation = time.Since(evaluationStart)

	postprocessStart := time.Now()
	result, err := dagbench.DecryptDecode(ctx, resultCipher)
	if err != nil {
		return fmt.Errorf("decrypt/decode result: %w", err)
	}
	timings.Postprocess = time.Since(postprocessStart)

	fmt.Println("Lattigo CKKS DAG manual-parallel benchmark")
	fmt.Printf("  LogN: %d\n", dagbench.DefaultLogN)
	fmt.Printf("  slots: %d\n", params.MaxSlots())
	fmt.Printf("  LogDefaultScale: %d\n", dagbench.DefaultLogScale)
	fmt.Printf("  rotations: %v\n", dagbench.Rotations)
	fmt.Printf("  branch goroutines: %d\n", dagbench.DefaultParallelism)
	fmt.Printf("  GOMAXPROCS: %d\n", gomaxprocs)
	fmt.Println("  multiply path: Mul -> Relinearize -> Rescale")
	fmt.Println()

	dagbench.PrintTimings("Manual-parallel", timings)
	dagbench.PrintDagTrace(dagTrace)
	fmt.Printf("Manual-parallel example total TIME: %s\n", time.Since(exampleStart))
	fmt.Println()

	dagbench.PrintHead("Expected head:", expected)
	dagbench.PrintHead("Manual-parallel result head:", result)
	dagbench.PrintPrecisionStats("Manual-parallel vs expected:", dagbench.ComputePrecisionStats(expected, result))

	return nil
}
