package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/tuneinsight/lattigo/v6/circuits/ckks/polynomial"
	"github.com/tuneinsight/lattigo/v6/core/rlwe"
	"github.com/tuneinsight/lattigo/v6/schemes/ckks"
	"github.com/tuneinsight/lattigo/v6/utils/bignum"
)

const (
	DataNums  = 100
	Dimension = 10
	N         = 16384
	Num       = 128
)

// ================== JSON 数据结构定义 ==================
type InputData struct {
	Query []float64   `json:"query"`
	Data  [][]float64 `json:"data"`
}

type OutputData struct {
	Answer []int `json:"answer"`
}

func main() {
	runtime.GOMAXPROCS(8)
	start := time.Now()

	// 1. 参数设置
	LogQ := make([]int, 18)
	for i := range LogQ {
		LogQ[i] = 32
	}
	LogP := []int{60}

	params, err := ckks.NewParametersFromLiteral(ckks.ParametersLiteral{
		LogN:            15,
		LogQ:            LogQ,
		LogP:            LogP,
		LogDefaultScale: 32,
	})
	if err != nil {
		panic(err)
	}

	// 2. 密钥生成
	kgen := rlwe.NewKeyGenerator(params)
	sk, pk := kgen.GenKeyPairNew()
	rlk := kgen.GenRelinearizationKeyNew(sk)

	rotations := []int{100, 200, 300, 600, 1200, 2500, 5000}
	galEls := params.GaloisElements(rotations)
	gks := GenGaloisKeysParallel(kgen, galEls, sk, 8)

	evk := rlwe.NewMemEvaluationKeySet(rlk, gks...)

	encoder := ckks.NewEncoder(params)
	encryptor := rlwe.NewEncryptor(params, pk)
	evaluator := ckks.NewEvaluator(params, evk)
	decryptor := rlwe.NewDecryptor(params, sk) // 新增：解密器

	// 3. 读取真实数据
	fmt.Println("Loading data from JSON...")
	query := make([][]complex128, 10)
	data := make([][]complex128, 20)
	for i := range query {
		query[i] = make([]complex128, params.MaxSlots())
	}
	for i := range data {
		data[i] = make([]complex128, params.MaxSlots())
	}

	loadData("train.jsonl", query, data) // 假设 train.jsonl 在上一级目录
	fmt.Printf("Init & Load done in %s\n", time.Since(start))

	calcStart := time.Now()

	// 4. 并发编码与加密
	ciphQuery := encodeAndEncryptMT(params, encoder, encryptor, query)
	ciphData := encodeAndEncryptMT(params, encoder, encryptor, data)

	// 5. sub_and_square
	subAndSquare(evaluator, ciphData, ciphQuery)

	// 6. 维度累加计算
	ciphDistance1 := ciphData[0].CopyNew()
	ciphDistance2 := ciphData[Dimension].CopyNew()
	for i := 1; i < Dimension; i++ {
		evaluator.Add(ciphDistance1, ciphData[i], ciphDistance1)
		evaluator.Add(ciphDistance2, ciphData[i+Dimension], ciphDistance2)
	}

	ciphResult, _ := evaluator.SubNew(ciphDistance1, ciphDistance2)

	// 7. 多项式评估 sign_1
	polyEval := polynomial.NewEvaluator(params, evaluator)
	coeffs := []float64{0, 3.816912, 0, -9.226954, 0, 11.954844, 0, -5.516258}
	polyFunc := bignum.NewPolynomial(bignum.Monomial, coeffs, nil)
	ciphResult, _ = polyEval.Evaluate(ciphResult, polyFunc, params.DefaultScale())

	// 8. 累加 Top N Block
	ciphResult = accumulateTopNBlock(evaluator, ciphResult, 100)

	// 9. 比较阈值 TopK 匹配
	cmpTopK := make([]complex128, params.MaxSlots())
	for i := 0; i < DataNums; i++ {
		cmpTopK[i] = complex(10.5, 0)
	}
	ptTopK := ckks.NewPlaintext(params, params.MaxLevel())
	encoder.Encode(cmpTopK, ptTopK)
	ciphTopK, _ := encryptor.EncryptNew(ptTopK)

	for ciphTopK.Level() > ciphResult.Level() {
		evaluator.DropLevel(ciphTopK, 1)
	}

	evaluator.Sub(ciphTopK, ciphResult, ciphResult)
	evaluator.Mul(ciphResult, float64(0.014), ciphResult)
	evaluator.Rescale(ciphResult, ciphResult)

	// 10. 随机掩码混淆
	randMask := make([]complex128, params.MaxSlots())
	for i := 0; i < DataNums; i++ {
		randMask[i] = complex(0.9+rand.Float64()*0.2, 0)
	}
	ptMask := ckks.NewPlaintext(params, ciphResult.Level())
	encoder.Encode(randMask, ptMask)

	evaluator.MulRelin(ciphResult, ptMask, ciphResult)
	evaluator.Rescale(ciphResult, ciphResult)

	calcEnd := time.Since(calcStart)

	// ================== 11. 解密与结果输出 ==================
	ptResult := decryptor.DecryptNew(ciphResult)
	resultVec := make([]complex128, params.MaxSlots())
	encoder.Decode(ptResult, resultVec)

	var finalResult []int
	for i := 0; i < 100; i++ {
		// 取实部进行四舍五入
		if math.Round(real(resultVec[i])) == 1 {
			finalResult = append(finalResult, i+1)
		}
	}

	// 写入结果
	writePredictions(finalResult, "../predictions.jsonl")

	fmt.Printf("Calculate Time: %s\n", calcEnd)
	fmt.Printf("Total Time: %s\n", time.Since(start))
	fmt.Printf("Predictions saved to ../predictions.jsonl. Matched items: %d\n", len(finalResult))
}

// ================== 数据加载与保存逻辑 ==================

func loadData(filename string, query [][]complex128, data [][]complex128) {
	file, err := os.Open(filename)
	if err != nil {
		panic(fmt.Sprintf("cannot open file: %v", err))
	}
	defer file.Close()

	var input InputData
	if err := json.NewDecoder(file).Decode(&input); err != nil {
		panic(fmt.Sprintf("json parsing error: %v", err))
	}

	// 填充 Query
	for i := 0; i < 10; i++ {
		for j := 0; j < 10000; j++ {
			query[i][j] = complex(input.Query[i]/40.0, 0)
		}
	}

	// 填充 Data
	numRows := len(input.Data)
	if numRows == 0 {
		return
	}
	numCols := len(input.Data[0])

	for r := 0; r < numRows; r++ {
		for copy := 0; copy < 100; copy++ {
			targetRow := r + copy*100
			for c := 0; c < numCols; c++ {
				data[c][targetRow] = complex(input.Data[r][c]/40.0, 0)
			}
		}
	}

	for r := 0; r < numRows; r++ {
		for copy := 0; copy < 100; copy++ {
			targetRow := r*100 + copy
			for c := 0; c < numCols; c++ {
				data[c+10][targetRow] = complex(input.Data[r][c]/40.0, 0)
			}
		}
	}
}

func writePredictions(data []int, filename string) {
	out := OutputData{Answer: make([]int, 10)}
	for i := 0; i < 10; i++ {
		if i < len(data) {
			out.Answer[i] = data[i]
		} else {
			out.Answer[i] = 100 // 占位填充
		}
	}

	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Failed to create file: %v\n", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	// 防止 JSON 自动转义字符等，这里按照原生结构输出
	if err := encoder.Encode(out); err != nil {
		fmt.Printf("Failed to write json: %v\n", err)
	}
}

// ================== 核心同态运算函数 ==================

func encodeAndEncryptMT(params ckks.Parameters, encoder *ckks.Encoder, encryptor *rlwe.Encryptor, message [][]complex128) []*rlwe.Ciphertext {
	ciphers := make([]*rlwe.Ciphertext, len(message))
	var wg sync.WaitGroup

	for i := range message {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pt := ckks.NewPlaintext(params, params.MaxLevel())
			encoder.Encode(message[idx], pt)
			ciphers[idx], _ = encryptor.EncryptNew(pt)
		}(i)
	}
	wg.Wait()
	return ciphers
}

func subAndSquare(evaluator *ckks.Evaluator, ciphData []*rlwe.Ciphertext, ciphQuery []*rlwe.Ciphertext) {
	var wg sync.WaitGroup
	querySize := len(ciphQuery)

	for i := 0; i < querySize; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			evaluator.Sub(ciphData[idx], ciphQuery[idx], ciphData[idx])
			evaluator.MulRelin(ciphData[idx], ciphData[idx], ciphData[idx])
			evaluator.Rescale(ciphData[idx], ciphData[idx])

			evaluator.Sub(ciphData[idx+Dimension], ciphQuery[idx], ciphData[idx+Dimension])
			evaluator.MulRelin(ciphData[idx+Dimension], ciphData[idx+Dimension], ciphData[idx+Dimension])
			evaluator.Rescale(ciphData[idx+Dimension], ciphData[idx+Dimension])
		}(i)
	}
	wg.Wait()
}

func accumulateTopNBlock(evaluator *ckks.Evaluator, ciph *rlwe.Ciphertext, n int) *rlwe.Ciphertext {
	ciphRotateSum := ciph.CopyNew()
	ciphSum, _ := evaluator.SubNew(ciph, ciph)

	for n > 1 {
		if n&1 != 0 && n != 1 {
			evaluator.Add(ciphSum, ciphRotateSum, ciphSum)
			ciphRotateSum, _ = evaluator.RotateNew(ciphRotateSum, 100)
			n--
		}
		n = n >> 1
		if n > 0 {
			ciphTmp, _ := evaluator.RotateNew(ciphRotateSum, n*100)
			evaluator.Add(ciphRotateSum, ciphTmp, ciphRotateSum)
		}
	}
	evaluator.Add(ciphSum, ciphRotateSum, ciphSum)
	return ciphSum
}

func GenGaloisKeysParallel(kgen *rlwe.KeyGenerator, galEls []uint64, sk *rlwe.SecretKey, maxThreads int) []*rlwe.GaloisKey {
	gks := make([]*rlwe.GaloisKey, len(galEls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxThreads)

	for i, galEl := range galEls {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, el uint64) {
			defer wg.Done()
			defer func() { <-sem }()
			gks[idx] = kgen.GenGaloisKeyNew(el, sk)
		}(i, galEl)
	}

	wg.Wait()
	return gks
}
