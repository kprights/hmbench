package dagbench

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/tuneinsight/lattigo/v6/core/rlwe"
	"github.com/tuneinsight/lattigo/v6/schemes/ckks"
)

const (
	WideDAG24ScaleDivisor = 64.0
	WideDAG48ScaleDivisor = 128.0
)

type WideDAGConfig struct {
	BranchCount    int
	ManualParallel bool
	ScaleDivisor   float64
	Seed           int64
}

type wideBranchSpec struct {
	lhs          int
	rhs          int
	preRotation  int
	postRotation int
}

func NewWideDAGConfig(branchCount int, manualParallel bool) WideDAGConfig {
	scaleDivisor := WideDAG24ScaleDivisor
	if branchCount >= 48 {
		scaleDivisor = WideDAG48ScaleDivisor
	}
	return WideDAGConfig{
		BranchCount:    branchCount,
		ManualParallel: manualParallel,
		ScaleDivisor:   scaleDivisor,
		Seed:           DefaultSeed,
	}
}

func GenerateScaledMessages(params ckks.Parameters, seed int64, divisor float64) Messages {
	rng := rand.New(rand.NewSource(seed))
	slots := params.MaxSlots()

	makeMessage := func() []complex128 {
		values := make([]complex128, slots)
		for i := range values {
			realPart := rng.Float64()*2 - 1
			imagPart := rng.Float64()*2 - 1
			values[i] = complex(realPart/divisor, imagPart/divisor)
		}
		return values
	}

	return Messages{
		A: makeMessage(),
		B: makeMessage(),
		C: makeMessage(),
		D: makeMessage(),
	}
}

func BuildWideReference(messages Messages, branchCount int) []complex128 {
	specs := wideBranchSpecs(branchCount)
	inputs := [][]complex128{messages.A, messages.B, messages.C, messages.D}
	branches := make([][]complex128, branchCount)
	for i, spec := range specs {
		branches[i] = wideBranchRef(inputs[spec.lhs], inputs[spec.rhs], spec.preRotation, spec.postRotation)
	}

	merged := append([]complex128(nil), branches[0]...)
	for i := 1; i < len(branches); i++ {
		merged = addRef(merged, branches[i])
	}

	tailProd := mulRef(merged, branches[0])
	tailRot32 := rotateLeftCopy(tailProd, 32)
	return addRef(tailProd, tailRot32)
}

func SingleThreadWideWorkloadWithTrace(eval *ckks.Evaluator, params ckks.Parameters, inputs CipherInputs, branchCount int, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	specs := wideBranchSpecs(branchCount)
	cipherInputs := []*rlwe.Ciphertext{inputs.A, inputs.B, inputs.C, inputs.D}
	branches := make([]*rlwe.Ciphertext, branchCount)

	for i, spec := range specs {
		branch, err := wideBranch(eval, params, cipherInputs[spec.lhs], cipherInputs[spec.rhs], spec.preRotation, spec.postRotation, wideBranchGroupName(i), trace)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", wideBranchGroupName(i), err)
		}
		branches[i] = branch
	}

	return wideFinalReduce(eval, params, branches, trace)
}

func ManualParallelWideWorkloadWithTrace(ctx *HEContext, inputs CipherInputs, branchCount int, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	specs := wideBranchSpecs(branchCount)
	cipherInputs := []*rlwe.Ciphertext{inputs.A, inputs.B, inputs.C, inputs.D}
	branches := make([]*rlwe.Ciphertext, branchCount)
	branchTraces := make([][]DagTraceItem, branchCount)
	errs := make([]error, branchCount)

	var wg sync.WaitGroup
	wg.Add(branchCount)
	for i, spec := range specs {
		i := i
		spec := spec
		go func() {
			defer wg.Done()
			eval := NewEvaluator(ctx)
			branches[i], errs[i] = wideBranch(eval, ctx.Params, cipherInputs[spec.lhs], cipherInputs[spec.rhs], spec.preRotation, spec.postRotation, wideBranchGroupName(i), &branchTraces[i])
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", wideBranchGroupName(i), err)
		}
	}

	for _, branchTrace := range branchTraces {
		appendTrace(trace, branchTrace)
	}

	return wideFinalReduce(NewEvaluator(ctx), ctx.Params, branches, trace)
}

func RunWideDAGExample(config WideDAGConfig) error {
	if config.BranchCount <= 0 {
		return fmt.Errorf("branch count must be > 0")
	}
	if config.ScaleDivisor <= 0 {
		return fmt.Errorf("scale divisor must be > 0")
	}

	exampleStart := time.Now()
	gomaxprocs := 1
	if config.ManualParallel {
		gomaxprocs = ConfigureGoMaxProcs(config.BranchCount)
	} else {
		gomaxprocs = ConfigureGoMaxProcs(1)
	}

	var timings Timings

	setupStart := time.Now()
	params, err := NewParameters()
	if err != nil {
		return fmt.Errorf("create CKKS parameters: %w", err)
	}
	timings.Setup = time.Since(setupStart)

	ctx, ctxTimings, err := NewHEContext(params)
	if err != nil {
		return fmt.Errorf("create HE context: %w", err)
	}
	timings.Keygen = ctxTimings.Keygen
	timings.RuntimeSetup = ctxTimings.RuntimeSetup

	messageStart := time.Now()
	messages := GenerateScaledMessages(params, config.Seed, config.ScaleDivisor)
	timings.MessagePrep = time.Since(messageStart)

	referenceStart := time.Now()
	expected := BuildWideReference(messages, config.BranchCount)
	timings.Reference = time.Since(referenceStart)

	inputs, ioTimings, err := EncodeEncrypt(ctx, messages)
	if err != nil {
		return err
	}
	timings.Encode = ioTimings.Encode
	timings.Encrypt = ioTimings.Encrypt

	var dagTrace []DagTraceItem
	evaluationStart := time.Now()
	var resultCipher *rlwe.Ciphertext
	if config.ManualParallel {
		resultCipher, err = ManualParallelWideWorkloadWithTrace(ctx, inputs, config.BranchCount, &dagTrace)
	} else {
		resultCipher, err = SingleThreadWideWorkloadWithTrace(NewEvaluator(ctx), params, inputs, config.BranchCount, &dagTrace)
	}
	if err != nil {
		return fmt.Errorf("evaluate wide DAG: %w", err)
	}
	timings.Evaluation = time.Since(evaluationStart)

	postprocessStart := time.Now()
	result, err := DecryptDecode(ctx, resultCipher)
	if err != nil {
		return fmt.Errorf("decrypt/decode result: %w", err)
	}
	timings.Postprocess = time.Since(postprocessStart)

	mode := "single-thread"
	if config.ManualParallel {
		mode = "manual-parallel"
	}

	fmt.Printf("Lattigo CKKS DAG %s %d-branch benchmark\n", mode, config.BranchCount)
	fmt.Printf("  LogN: %d\n", DefaultLogN)
	fmt.Printf("  slots: %d\n", params.MaxSlots())
	fmt.Printf("  LogDefaultScale: %d\n", DefaultLogScale)
	fmt.Printf("  rotations: %v\n", Rotations)
	fmt.Printf("  branch count: %d\n", config.BranchCount)
	fmt.Printf("  message divisor: %.1f\n", config.ScaleDivisor)
	if config.ManualParallel {
		fmt.Printf("  branch goroutines: %d\n", config.BranchCount)
	}
	fmt.Printf("  GOMAXPROCS: %d\n", gomaxprocs)
	fmt.Printf("  runtime.NumCPU: %d\n", runtime.NumCPU())
	fmt.Println("  multiply path: Mul -> Relinearize -> Rescale")
	fmt.Println()

	printWideTimings(mode, config.BranchCount, timings)
	PrintWideDagTrace(dagTrace, config.BranchCount)
	fmt.Printf("%s %d-branch example total TIME: %.3f ms\n", mode, config.BranchCount, durationMS(time.Since(exampleStart)))
	fmt.Println()

	PrintHead("Expected head:", expected)
	PrintHead(fmt.Sprintf("%s result head:", mode), result)
	PrintPrecisionStats(fmt.Sprintf("%s vs expected:", mode), ComputePrecisionStats(expected, result))

	return nil
}

func PrintWideDagTrace(trace []DagTraceItem, branchCount int) {
	fmt.Println("CKKS DAG group timing:")
	var branchTotal time.Duration
	for i := 0; i < branchCount; i++ {
		group := wideBranchGroupName(i)
		groupTotal := traceGroupTotalDuration(trace, group)
		branchTotal += groupTotal
		fmt.Printf("  %s TIME: %.3f ms\n", group, durationMS(groupTotal))
	}
	fmt.Printf("  parallel_%d_branches TOTAL_TIME: %.3f ms\n", branchCount, durationMS(branchTotal))
	fmt.Printf("  merge_tail TIME: %.3f ms\n", durationMS(traceGroupTotalDuration(trace, "merge_tail")))

	fmt.Println("CKKS DAG operation timing:")
	for _, item := range trace {
		fmt.Printf("  [%s] %s TIME: %.3f ms", item.Group, item.Op, durationMS(item.Duration))
		if item.HasCipherState {
			fmt.Printf(" | level %d -> %d, scale %.6e -> %.6e, log2(scale) %.2f -> %.2f",
				item.LevelBefore,
				item.LevelAfter,
				item.ScaleBefore,
				item.ScaleAfter,
				item.LogScaleBefore,
				item.LogScaleAfter)
		}
		fmt.Println()
	}
}

func wideBranchSpecs(branchCount int) []wideBranchSpec {
	inputPairs := [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}
	rotations := []int{1, 2, 4, 8, 16, 32}
	specs := make([]wideBranchSpec, branchCount)

	for i := 0; i < branchCount; i++ {
		lane := i % len(inputPairs)
		cycle := i / len(inputPairs)
		pair := inputPairs[lane]
		specs[i] = wideBranchSpec{
			lhs:          pair[0],
			rhs:          pair[1],
			preRotation:  rotations[(lane+cycle)%len(rotations)],
			postRotation: rotations[(lane+cycle+2)%len(rotations)],
		}
	}
	return specs
}

func wideBranchGroupName(index int) string {
	return fmt.Sprintf("branch_%02d", index)
}

func wideBranchRef(lhs, rhs []complex128, preRotation, postRotation int) []complex128 {
	inputSum := addRef(lhs, rhs)
	preRot := rotateLeftCopy(inputSum, preRotation)
	mixed := addRef(inputSum, preRot)
	squared := mulRef(mixed, mixed)
	postRot := rotateLeftCopy(squared, postRotation)
	return addRef(squared, postRot)
}

func wideBranch(eval *ckks.Evaluator, params ckks.Parameters, lhs, rhs *rlwe.Ciphertext, preRotation, postRotation int, group string, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	var inputSum *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_inputs", func() error {
		var err error
		inputSum, err = eval.AddNew(lhs, rhs)
		return err
	}); err != nil {
		return nil, err
	}

	var preRot *rlwe.Ciphertext
	if err := recordOp(trace, group, fmt.Sprintf("rotate_pre_%d", preRotation), func() error {
		var err error
		preRot, err = eval.RotateNew(inputSum, preRotation)
		return err
	}); err != nil {
		return nil, err
	}

	var mixed *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_pre_rot", func() error {
		var err error
		mixed, err = eval.AddNew(inputSum, preRot)
		return err
	}); err != nil {
		return nil, err
	}

	squared, err := multiplyRelinearizeRescale(eval, params, mixed, mixed, trace, group, "square")
	if err != nil {
		return nil, err
	}

	var postRot *rlwe.Ciphertext
	if err := recordOp(trace, group, fmt.Sprintf("rotate_post_%d", postRotation), func() error {
		var err error
		postRot, err = eval.RotateNew(squared, postRotation)
		return err
	}); err != nil {
		return nil, err
	}

	var branch *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_post_rot", func() error {
		var err error
		branch, err = eval.AddNew(squared, postRot)
		return err
	}); err != nil {
		return nil, err
	}
	return branch, nil
}

func wideFinalReduce(eval *ckks.Evaluator, params ckks.Parameters, branches []*rlwe.Ciphertext, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	if len(branches) == 0 {
		return nil, fmt.Errorf("no branches to reduce")
	}

	const group = "merge_tail"
	var merged *rlwe.Ciphertext
	if err := recordOp(trace, group, "copy_branch_00", func() error {
		merged = branches[0].CopyNew()
		return nil
	}); err != nil {
		return nil, err
	}

	for i := 1; i < len(branches); i++ {
		i := i
		if err := recordOp(trace, group, fmt.Sprintf("add_branch_%02d", i), func() error {
			return eval.Add(merged, branches[i], merged)
		}); err != nil {
			return nil, err
		}
	}

	tailProd, err := multiplyRelinearizeRescale(eval, params, merged, branches[0], trace, group, "multiply_tail_branch_00")
	if err != nil {
		return nil, err
	}

	var tailRot32 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_32", func() error {
		var err error
		tailRot32, err = eval.RotateNew(tailProd, 32)
		return err
	}); err != nil {
		return nil, err
	}

	var result *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_32", func() error {
		var err error
		result, err = eval.AddNew(tailProd, tailRot32)
		return err
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func traceGroupTotalDuration(trace []DagTraceItem, group string) time.Duration {
	var total time.Duration
	for _, item := range trace {
		if item.Group == group {
			total += item.Duration
		}
	}
	return total
}

func printWideTimings(mode string, branchCount int, timings Timings) {
	prefix := fmt.Sprintf("%s %d-branch", mode, branchCount)
	fmt.Printf("%s CKKS setup TIME: %.3f ms\n", prefix, durationMS(timings.Setup))
	fmt.Printf("%s CKKS key generation TIME: %.3f ms\n", prefix, durationMS(timings.Keygen))
	fmt.Printf("%s CKKS runtime object setup TIME: %.3f ms\n", prefix, durationMS(timings.RuntimeSetup))
	fmt.Printf("%s Message preparation TIME: %.3f ms\n", prefix, durationMS(timings.MessagePrep))
	fmt.Printf("%s Plaintext reference TIME: %.3f ms\n", prefix, durationMS(timings.Reference))
	fmt.Printf("%s CKKS encode TIME: %.3f ms\n", prefix, durationMS(timings.Encode))
	fmt.Printf("%s CKKS encrypt TIME: %.3f ms\n", prefix, durationMS(timings.Encrypt))
	fmt.Printf("%s CKKS DAG evaluation TIME: %.3f ms\n", prefix, durationMS(timings.Evaluation))
	fmt.Printf("%s CKKS decrypt/decode TIME: %.3f ms\n", prefix, durationMS(timings.Postprocess))
	fmt.Printf("%s CKKS full pipeline TIME: %.3f ms\n", prefix, durationMS(timings.FullPipeline()))
}
