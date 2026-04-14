package main

import (
	"fmt"
	"os"
	"time"

	"he_knn_lattigo/internal/dagbench"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ckks_dag_single_thread failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	exampleStart := time.Now()
	gomaxprocs := dagbench.ConfigureGoMaxProcs(1)

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

	evaluator := dagbench.NewEvaluator(ctx)
	var dagTrace []dagbench.DagTraceItem
	evaluationStart := time.Now()
	resultCipher, err := dagbench.SingleThreadWorkloadWithTrace(evaluator, params, inputs, &dagTrace)
	if err != nil {
		return fmt.Errorf("evaluate single-thread DAG: %w", err)
	}
	timings.Evaluation = time.Since(evaluationStart)

	postprocessStart := time.Now()
	result, err := dagbench.DecryptDecode(ctx, resultCipher)
	if err != nil {
		return fmt.Errorf("decrypt/decode result: %w", err)
	}
	timings.Postprocess = time.Since(postprocessStart)

	fmt.Println("Lattigo CKKS DAG single-thread benchmark")
	fmt.Printf("  LogN: %d\n", dagbench.DefaultLogN)
	fmt.Printf("  slots: %d\n", params.MaxSlots())
	fmt.Printf("  LogDefaultScale: %d\n", dagbench.DefaultLogScale)
	fmt.Printf("  rotations: %v\n", dagbench.Rotations)
	fmt.Printf("  GOMAXPROCS: %d\n", gomaxprocs)
	fmt.Println("  multiply path: Mul -> Relinearize -> Rescale")
	fmt.Println()

	dagbench.PrintTimings("Single-thread", timings)
	dagbench.PrintDagTrace(dagTrace)
	fmt.Printf("Single-thread example total TIME: %s\n", time.Since(exampleStart))
	fmt.Println()

	dagbench.PrintHead("Expected head:", expected)
	dagbench.PrintHead("Single-thread result head:", result)
	dagbench.PrintPrecisionStats("Single-thread vs expected:", dagbench.ComputePrecisionStats(expected, result))

	return nil
}
