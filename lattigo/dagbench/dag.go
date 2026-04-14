package dagbench

import (
	"fmt"
	"math"
	"math/cmplx"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/tuneinsight/lattigo/v6/core/rlwe"
	"github.com/tuneinsight/lattigo/v6/schemes/ckks"
)

const (
	DefaultLogN        = 15
	DefaultLogScale    = 40
	DefaultSeed        = int64(0x5eed2026)
	DefaultParallelism = 3
)

var (
	Rotations = []int{1, 2, 4, 8, 16, 32}
	LogQ      = []int{40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40}
	LogP      = []int{60}
)

type Timings struct {
	Setup        time.Duration
	Keygen       time.Duration
	RuntimeSetup time.Duration
	MessagePrep  time.Duration
	Reference    time.Duration
	Encode       time.Duration
	Encrypt      time.Duration
	Evaluation   time.Duration
	Postprocess  time.Duration
}

type DagTraceItem struct {
	Group          string
	Op             string
	Duration       time.Duration
	HasCipherState bool
	LevelBefore    int
	LevelAfter     int
	ScaleBefore    float64
	ScaleAfter     float64
	LogScaleBefore float64
	LogScaleAfter  float64
}

func (t Timings) FullPipeline() time.Duration {
	return t.Setup + t.Keygen + t.RuntimeSetup + t.MessagePrep + t.Encode + t.Encrypt + t.Evaluation + t.Postprocess
}

type HEContext struct {
	Params    ckks.Parameters
	SecretKey *rlwe.SecretKey
	PublicKey *rlwe.PublicKey
	Evk       *rlwe.MemEvaluationKeySet
	Encoder   *ckks.Encoder
	Encryptor *rlwe.Encryptor
	Decryptor *rlwe.Decryptor
}

type Messages struct {
	A []complex128
	B []complex128
	C []complex128
	D []complex128
}

type CipherInputs struct {
	A *rlwe.Ciphertext
	B *rlwe.Ciphertext
	C *rlwe.Ciphertext
	D *rlwe.Ciphertext
}

type PrecisionStats struct {
	MaxAbsError      float64
	MeanAbsError     float64
	MaxRelError      float64
	MinPrecisionBits float64
}

func EnvInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func ConfigureGoMaxProcs(defaultValue int) int {
	requested := EnvInt("LATTIGO_DAG_GOMAXPROCS", defaultValue)
	if requested < 1 {
		requested = 1
	}
	runtime.GOMAXPROCS(requested)
	return requested
}

func NewParameters() (ckks.Parameters, error) {
	return ckks.NewParametersFromLiteral(ckks.ParametersLiteral{
		LogN:            DefaultLogN,
		LogQ:            LogQ,
		LogP:            LogP,
		LogDefaultScale: DefaultLogScale,
	})
}

func NewHEContext(params ckks.Parameters) (*HEContext, Timings, error) {
	var timings Timings

	keygenStart := time.Now()
	kgen := rlwe.NewKeyGenerator(params)
	sk, pk := kgen.GenKeyPairNew()
	rlk := kgen.GenRelinearizationKeyNew(sk)
	galEls := params.GaloisElements(Rotations)
	gks := make([]*rlwe.GaloisKey, len(galEls))
	for i, galEl := range galEls {
		gks[i] = kgen.GenGaloisKeyNew(galEl, sk)
	}
	timings.Keygen = time.Since(keygenStart)

	runtimeStart := time.Now()
	ctx := &HEContext{
		Params:    params,
		SecretKey: sk,
		PublicKey: pk,
		Evk:       rlwe.NewMemEvaluationKeySet(rlk, gks...),
		Encoder:   ckks.NewEncoder(params),
		Encryptor: rlwe.NewEncryptor(params, pk),
		Decryptor: rlwe.NewDecryptor(params, sk),
	}
	timings.RuntimeSetup = time.Since(runtimeStart)

	return ctx, timings, nil
}

func NewEvaluator(ctx *HEContext) *ckks.Evaluator {
	return ckks.NewEvaluator(ctx.Params, ctx.Evk)
}

func GenerateMessages(params ckks.Parameters, seed int64) Messages {
	rng := rand.New(rand.NewSource(seed))
	slots := params.MaxSlots()

	makeMessage := func() []complex128 {
		values := make([]complex128, slots)
		for i := range values {
			realPart := rng.Float64()*2 - 1
			imagPart := rng.Float64()*2 - 1
			values[i] = complex(realPart/8.0, imagPart/8.0)
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

func EncodeEncrypt(ctx *HEContext, messages Messages) (CipherInputs, Timings, error) {
	var timings Timings

	encodeOne := func(values []complex128) (*rlwe.Plaintext, error) {
		pt := ckks.NewPlaintext(ctx.Params, ctx.Params.MaxLevel())
		return pt, ctx.Encoder.Encode(values, pt)
	}

	encodeStart := time.Now()
	ptA, err := encodeOne(messages.A)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encode A: %w", err)
	}
	ptB, err := encodeOne(messages.B)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encode B: %w", err)
	}
	ptC, err := encodeOne(messages.C)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encode C: %w", err)
	}
	ptD, err := encodeOne(messages.D)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encode D: %w", err)
	}
	timings.Encode = time.Since(encodeStart)

	encryptStart := time.Now()
	ctA, err := ctx.Encryptor.EncryptNew(ptA)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encrypt A: %w", err)
	}
	ctB, err := ctx.Encryptor.EncryptNew(ptB)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encrypt B: %w", err)
	}
	ctC, err := ctx.Encryptor.EncryptNew(ptC)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encrypt C: %w", err)
	}
	ctD, err := ctx.Encryptor.EncryptNew(ptD)
	if err != nil {
		return CipherInputs{}, timings, fmt.Errorf("encrypt D: %w", err)
	}
	timings.Encrypt = time.Since(encryptStart)

	return CipherInputs{A: ctA, B: ctB, C: ctC, D: ctD}, timings, nil
}

func BuildReference(messages Messages) []complex128 {
	branchAdd := smoothSquareBranchRef(messages.A, messages.B)
	branchQuad := diffEnergyBranchRef(messages.C, messages.D)
	branchCross := crossMixBranchRef(messages.A, messages.B, messages.C, messages.D)

	mergedLeft := addRef(branchAdd, branchQuad)
	mergedAll := addRef(mergedLeft, branchCross)
	tailProd := mulRef(mergedAll, branchAdd)
	tailRot32 := rotateLeftCopy(tailProd, 32)
	return addRef(tailProd, tailRot32)
}

func recordOp(trace *[]DagTraceItem, group, op string, fn func() error) error {
	start := time.Now()
	err := fn()
	elapsed := time.Since(start)
	if trace != nil {
		*trace = append(*trace, DagTraceItem{
			Group:    group,
			Op:       op,
			Duration: elapsed,
		})
	}
	return err
}

func recordRescale(eval *ckks.Evaluator, cipher *rlwe.Ciphertext, trace *[]DagTraceItem, group string) error {
	levelBefore := cipher.Level()
	scaleBefore := cipher.Scale.Float64()
	logScaleBefore := cipher.Scale.Log2()

	start := time.Now()
	err := eval.Rescale(cipher, cipher)
	elapsed := time.Since(start)

	if trace != nil {
		*trace = append(*trace, DagTraceItem{
			Group:          group,
			Op:             "rescale",
			Duration:       elapsed,
			HasCipherState: true,
			LevelBefore:    levelBefore,
			LevelAfter:     cipher.Level(),
			ScaleBefore:    scaleBefore,
			ScaleAfter:     cipher.Scale.Float64(),
			LogScaleBefore: logScaleBefore,
			LogScaleAfter:  cipher.Scale.Log2(),
		})
	}
	return err
}

func appendTrace(trace *[]DagTraceItem, items []DagTraceItem) {
	if trace != nil {
		*trace = append(*trace, items...)
	}
}

func SingleThreadWorkload(eval *ckks.Evaluator, params ckks.Parameters, inputs CipherInputs) (*rlwe.Ciphertext, error) {
	return SingleThreadWorkloadWithTrace(eval, params, inputs, nil)
}

func SingleThreadWorkloadWithTrace(eval *ckks.Evaluator, params ckks.Parameters, inputs CipherInputs, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	branchAdd, err := smoothSquareBranch(eval, params, inputs.A, inputs.B, trace)
	if err != nil {
		return nil, fmt.Errorf("smooth square branch: %w", err)
	}
	branchQuad, err := diffEnergyBranch(eval, params, inputs.C, inputs.D, trace)
	if err != nil {
		return nil, fmt.Errorf("diff energy branch: %w", err)
	}
	branchCross, err := crossMixBranch(eval, params, inputs.A, inputs.B, inputs.C, inputs.D, trace)
	if err != nil {
		return nil, fmt.Errorf("cross mix branch: %w", err)
	}
	return finalReduce(eval, params, branchAdd, branchQuad, branchCross, trace)
}

func ManualParallelWorkload(ctx *HEContext, inputs CipherInputs) (*rlwe.Ciphertext, error) {
	return ManualParallelWorkloadWithTrace(ctx, inputs, nil)
}

func ManualParallelWorkloadWithTrace(ctx *HEContext, inputs CipherInputs, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	evalAdd := NewEvaluator(ctx)
	evalQuad := NewEvaluator(ctx)
	evalCross := NewEvaluator(ctx)
	evalReduce := NewEvaluator(ctx)

	var wg sync.WaitGroup
	var branchAdd *rlwe.Ciphertext
	var branchQuad *rlwe.Ciphertext
	var branchCross *rlwe.Ciphertext
	var errAdd error
	var errQuad error
	var errCross error
	var branchAddTrace []DagTraceItem
	var branchQuadTrace []DagTraceItem
	var branchCrossTrace []DagTraceItem

	wg.Add(3)
	go func() {
		defer wg.Done()
		branchAdd, errAdd = smoothSquareBranch(evalAdd, ctx.Params, inputs.A, inputs.B, &branchAddTrace)
	}()
	go func() {
		defer wg.Done()
		branchQuad, errQuad = diffEnergyBranch(evalQuad, ctx.Params, inputs.C, inputs.D, &branchQuadTrace)
	}()
	go func() {
		defer wg.Done()
		branchCross, errCross = crossMixBranch(evalCross, ctx.Params, inputs.A, inputs.B, inputs.C, inputs.D, &branchCrossTrace)
	}()
	wg.Wait()

	if errAdd != nil {
		return nil, fmt.Errorf("smooth square branch: %w", errAdd)
	}
	if errQuad != nil {
		return nil, fmt.Errorf("diff energy branch: %w", errQuad)
	}
	if errCross != nil {
		return nil, fmt.Errorf("cross mix branch: %w", errCross)
	}

	appendTrace(trace, branchAddTrace)
	appendTrace(trace, branchQuadTrace)
	appendTrace(trace, branchCrossTrace)

	return finalReduce(evalReduce, ctx.Params, branchAdd, branchQuad, branchCross, trace)
}

func SmoothSquareBranch(eval *ckks.Evaluator, params ckks.Parameters, ctA, ctB *rlwe.Ciphertext) (*rlwe.Ciphertext, error) {
	return smoothSquareBranch(eval, params, ctA, ctB, nil)
}

func smoothSquareBranch(eval *ckks.Evaluator, params ckks.Parameters, ctA, ctB *rlwe.Ciphertext, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	const group = "branch_add"
	var sumAB *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_a_b", func() error {
		var err error
		sumAB, err = eval.AddNew(ctA, ctB)
		return err
	}); err != nil {
		return nil, err
	}
	var rotAB1 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_1", func() error {
		var err error
		rotAB1, err = eval.RotateNew(sumAB, 1)
		return err
	}); err != nil {
		return nil, err
	}
	var smoothAB *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_1", func() error {
		var err error
		smoothAB, err = eval.AddNew(sumAB, rotAB1)
		return err
	}); err != nil {
		return nil, err
	}
	smoothSq, err := multiplyRelinearizeRescale(eval, params, smoothAB, smoothAB, trace, group, "square")
	if err != nil {
		return nil, err
	}
	var smoothSqRot4 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_4", func() error {
		var err error
		smoothSqRot4, err = eval.RotateNew(smoothSq, 4)
		return err
	}); err != nil {
		return nil, err
	}
	var branchAdd *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_4", func() error {
		var err error
		branchAdd, err = eval.AddNew(smoothSq, smoothSqRot4)
		return err
	}); err != nil {
		return nil, err
	}
	return branchAdd, nil
}

func DiffEnergyBranch(eval *ckks.Evaluator, params ckks.Parameters, ctC, ctD *rlwe.Ciphertext) (*rlwe.Ciphertext, error) {
	return diffEnergyBranch(eval, params, ctC, ctD, nil)
}

func diffEnergyBranch(eval *ckks.Evaluator, params ckks.Parameters, ctC, ctD *rlwe.Ciphertext, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	const group = "branch_quad"
	var diffCD *rlwe.Ciphertext
	if err := recordOp(trace, group, "sub_c_d", func() error {
		var err error
		diffCD, err = eval.SubNew(ctC, ctD)
		return err
	}); err != nil {
		return nil, err
	}
	var diffRot2 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_2", func() error {
		var err error
		diffRot2, err = eval.RotateNew(diffCD, 2)
		return err
	}); err != nil {
		return nil, err
	}
	var diffMix *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_2", func() error {
		var err error
		diffMix, err = eval.AddNew(diffCD, diffRot2)
		return err
	}); err != nil {
		return nil, err
	}
	diffSq, err := multiplyRelinearizeRescale(eval, params, diffMix, diffMix, trace, group, "square")
	if err != nil {
		return nil, err
	}
	var diffSqRot8 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_8", func() error {
		var err error
		diffSqRot8, err = eval.RotateNew(diffSq, 8)
		return err
	}); err != nil {
		return nil, err
	}
	var branchQuad *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_8", func() error {
		var err error
		branchQuad, err = eval.AddNew(diffSq, diffSqRot8)
		return err
	}); err != nil {
		return nil, err
	}
	return branchQuad, nil
}

func CrossMixBranch(eval *ckks.Evaluator, params ckks.Parameters, ctA, ctB, ctC, ctD *rlwe.Ciphertext) (*rlwe.Ciphertext, error) {
	return crossMixBranch(eval, params, ctA, ctB, ctC, ctD, nil)
}

func crossMixBranch(eval *ckks.Evaluator, params ckks.Parameters, ctA, ctB, ctC, ctD *rlwe.Ciphertext, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	const group = "branch_cross"
	prodAC, err := multiplyRelinearizeRescale(eval, params, ctA, ctC, trace, group, "multiply_a_c")
	if err != nil {
		return nil, err
	}
	prodBD, err := multiplyRelinearizeRescale(eval, params, ctB, ctD, trace, group, "multiply_b_d")
	if err != nil {
		return nil, err
	}
	var crossSum *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_products", func() error {
		var err error
		crossSum, err = eval.AddNew(prodAC, prodBD)
		return err
	}); err != nil {
		return nil, err
	}
	var crossRot8 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_8", func() error {
		var err error
		crossRot8, err = eval.RotateNew(crossSum, 8)
		return err
	}); err != nil {
		return nil, err
	}
	var crossMix *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_8", func() error {
		var err error
		crossMix, err = eval.AddNew(crossSum, crossRot8)
		return err
	}); err != nil {
		return nil, err
	}
	var crossRot16 *rlwe.Ciphertext
	if err := recordOp(trace, group, "rotate_16", func() error {
		var err error
		crossRot16, err = eval.RotateNew(crossMix, 16)
		return err
	}); err != nil {
		return nil, err
	}
	var branchCross *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_rot_16", func() error {
		var err error
		branchCross, err = eval.AddNew(crossMix, crossRot16)
		return err
	}); err != nil {
		return nil, err
	}
	return branchCross, nil
}

func FinalReduce(eval *ckks.Evaluator, params ckks.Parameters, branchAdd, branchQuad, branchCross *rlwe.Ciphertext) (*rlwe.Ciphertext, error) {
	return finalReduce(eval, params, branchAdd, branchQuad, branchCross, nil)
}

func finalReduce(eval *ckks.Evaluator, params ckks.Parameters, branchAdd, branchQuad, branchCross *rlwe.Ciphertext, trace *[]DagTraceItem) (*rlwe.Ciphertext, error) {
	const group = "merge_tail"
	var mergedLeft *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_branch_quad", func() error {
		var err error
		mergedLeft, err = eval.AddNew(branchAdd, branchQuad)
		return err
	}); err != nil {
		return nil, err
	}
	var mergedAll *rlwe.Ciphertext
	if err := recordOp(trace, group, "add_branch_cross", func() error {
		var err error
		mergedAll, err = eval.AddNew(mergedLeft, branchCross)
		return err
	}); err != nil {
		return nil, err
	}
	tailProd, err := multiplyRelinearizeRescale(eval, params, mergedAll, branchAdd, trace, group, "multiply_tail")
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

func DecryptDecode(ctx *HEContext, cipher *rlwe.Ciphertext) ([]complex128, error) {
	plain := ctx.Decryptor.DecryptNew(cipher)
	values := make([]complex128, ctx.Params.MaxSlots())
	return values, ctx.Encoder.Decode(plain, values)
}

func ComputePrecisionStats(expected, actual []complex128) PrecisionStats {
	n := len(expected)
	if len(actual) < n {
		n = len(actual)
	}

	var stats PrecisionStats
	stats.MinPrecisionBits = math.Inf(1)
	for i := 0; i < n; i++ {
		absErr := cmplx.Abs(actual[i] - expected[i])
		refMag := cmplx.Abs(expected[i])
		relErr := absErr
		if refMag > 0 {
			relErr = absErr / refMag
		}

		stats.MeanAbsError += absErr
		if absErr > stats.MaxAbsError {
			stats.MaxAbsError = absErr
		}
		if relErr > stats.MaxRelError {
			stats.MaxRelError = relErr
		}
		if absErr > 0 {
			precisionBits := -math.Log2(absErr)
			if precisionBits < stats.MinPrecisionBits {
				stats.MinPrecisionBits = precisionBits
			}
		}
	}

	if n > 0 {
		stats.MeanAbsError /= float64(n)
	}
	if math.IsInf(stats.MinPrecisionBits, 1) {
		stats.MinPrecisionBits = math.Inf(1)
	}
	return stats
}

func PrintTimings(prefix string, timings Timings) {
	fmt.Printf("%s CKKS setup TIME: %s\n", prefix, timings.Setup)
	fmt.Printf("%s CKKS key generation TIME: %s\n", prefix, timings.Keygen)
	fmt.Printf("%s CKKS runtime object setup TIME: %s\n", prefix, timings.RuntimeSetup)
	fmt.Printf("%s Message preparation TIME: %s\n", prefix, timings.MessagePrep)
	fmt.Printf("%s Plaintext reference TIME: %s\n", prefix, timings.Reference)
	fmt.Printf("%s CKKS encode TIME: %s\n", prefix, timings.Encode)
	fmt.Printf("%s CKKS encrypt TIME: %s\n", prefix, timings.Encrypt)
	fmt.Printf("%s CKKS DAG evaluation TIME: %s\n", prefix, timings.Evaluation)
	fmt.Printf("%s CKKS decrypt/decode TIME: %s\n", prefix, timings.Postprocess)
	fmt.Printf("%s CKKS full pipeline TIME: %s\n", prefix, timings.FullPipeline())
}

func PrintDagTrace(trace []DagTraceItem) {
	groups := []string{"branch_add", "branch_quad", "branch_cross", "merge_tail"}

	fmt.Println("CKKS DAG group timing:")
	for _, group := range groups {
		fmt.Printf("  %s TIME: %.3f ms\n", group, traceGroupTotalMS(trace, group))
	}

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

func traceGroupTotalMS(trace []DagTraceItem, group string) float64 {
	var total time.Duration
	for _, item := range trace {
		if item.Group == group {
			total += item.Duration
		}
	}
	return durationMS(total)
}

func durationMS(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func PrintHead(label string, values []complex128) {
	fmt.Println(label)
	for i := 0; i < 4 && i < len(values); i++ {
		fmt.Printf("  value[%d] : %.10f + %.10f I\n", i, real(values[i]), imag(values[i]))
	}
}

func PrintPrecisionStats(label string, stats PrecisionStats) {
	fmt.Println(label)
	fmt.Printf("  max abs error      : %.6e\n", stats.MaxAbsError)
	fmt.Printf("  mean abs error     : %.6e\n", stats.MeanAbsError)
	fmt.Printf("  max relative error : %.6e\n", stats.MaxRelError)
	fmt.Printf("  min precision bits : %.2f\n", stats.MinPrecisionBits)
}

func multiplyRelinearizeRescale(eval *ckks.Evaluator, params ckks.Parameters, lhs, rhs *rlwe.Ciphertext, trace *[]DagTraceItem, group, multiplyOp string) (*rlwe.Ciphertext, error) {
	level := lhs.Level()
	if rhs.Level() < level {
		level = rhs.Level()
	}

	product := ckks.NewCiphertext(params, 2, level)
	if err := recordOp(trace, group, multiplyOp, func() error {
		return eval.Mul(lhs, rhs, product)
	}); err != nil {
		return nil, fmt.Errorf("multiply: %w", err)
	}
	var relinearized *rlwe.Ciphertext
	if err := recordOp(trace, group, "relinearize", func() error {
		var err error
		relinearized, err = eval.RelinearizeNew(product)
		return err
	}); err != nil {
		return nil, fmt.Errorf("relinearize: %w", err)
	}
	if err := recordRescale(eval, relinearized, trace, group); err != nil {
		return nil, fmt.Errorf("rescale: %w", err)
	}
	return relinearized, nil
}

func rotateLeftCopy(input []complex128, step int) []complex128 {
	if len(input) == 0 {
		return nil
	}
	result := append([]complex128(nil), input...)
	step %= len(result)
	copy(result, append(input[step:], input[:step]...))
	return result
}

func addRef(lhs, rhs []complex128) []complex128 {
	result := make([]complex128, len(lhs))
	for i := range lhs {
		result[i] = lhs[i] + rhs[i]
	}
	return result
}

func subRef(lhs, rhs []complex128) []complex128 {
	result := make([]complex128, len(lhs))
	for i := range lhs {
		result[i] = lhs[i] - rhs[i]
	}
	return result
}

func mulRef(lhs, rhs []complex128) []complex128 {
	result := make([]complex128, len(lhs))
	for i := range lhs {
		result[i] = lhs[i] * rhs[i]
	}
	return result
}

func smoothSquareBranchRef(msgA, msgB []complex128) []complex128 {
	sumAB := addRef(msgA, msgB)
	rotAB1 := rotateLeftCopy(sumAB, 1)
	smoothAB := addRef(sumAB, rotAB1)
	smoothSq := mulRef(smoothAB, smoothAB)
	smoothSqRot4 := rotateLeftCopy(smoothSq, 4)
	return addRef(smoothSq, smoothSqRot4)
}

func diffEnergyBranchRef(msgC, msgD []complex128) []complex128 {
	diffCD := subRef(msgC, msgD)
	diffRot2 := rotateLeftCopy(diffCD, 2)
	diffMix := addRef(diffCD, diffRot2)
	diffSq := mulRef(diffMix, diffMix)
	diffSqRot8 := rotateLeftCopy(diffSq, 8)
	return addRef(diffSq, diffSqRot8)
}

func crossMixBranchRef(msgA, msgB, msgC, msgD []complex128) []complex128 {
	prodAC := mulRef(msgA, msgC)
	prodBD := mulRef(msgB, msgD)
	crossSum := addRef(prodAC, prodBD)
	crossRot8 := rotateLeftCopy(crossSum, 8)
	crossMix := addRef(crossSum, crossRot8)
	crossRot16 := rotateLeftCopy(crossMix, 16)
	return addRef(crossMix, crossRot16)
}
