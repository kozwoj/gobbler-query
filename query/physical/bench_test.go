package physical

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
	"github.com/kozwoj/gobbler-query/query/source"
)

// ---- synthetic data generators ----------------------------------------------

const benchSeed = 42

var regions = []string{"eastus", "westus", "northeurope", "southeastasia", ""}

// genStringInt32Batches returns batches totalling n rows with two columns:
//   col 0 "key"   string — chosen from vals using rng
//   col 1 "value" int32  — sequential
func genStringInt32Batches(n, batchSize int, vals []string, rng *rand.Rand) []*batch.Batch {
	var batches []*batch.Batch
	for produced := 0; produced < n; {
		size := batchSize
		if produced+size > n {
			size = n - produced
		}
		keys := make([]string, size)
		ints := make([]int32, size)
		for i := range keys {
			keys[i] = vals[rng.IntN(len(vals))]
			ints[i] = int32(produced + i)
		}
		batches = append(batches, &batch.Batch{
			Length: size,
			Schema: []batch.ColumnMeta{
				{Name: "key", Origin: "t"},
				{Name: "value", Origin: "t"},
			},
			Columns: []batch.ColumnVector{
				&batch.StringVector{Values: keys},
				&batch.Int32Vector{Values: ints},
			},
		})
		produced += size
	}
	return batches
}

// genFloat64Batches returns batches totalling n rows with one float64 column.
func genFloat64Batches(n, batchSize int, rng *rand.Rand) []*batch.Batch {
	var batches []*batch.Batch
	for produced := 0; produced < n; {
		size := batchSize
		if produced+size > n {
			size = n - produced
		}
		vals := make([]float64, size)
		for i := range vals {
			vals[i] = rng.Float64() * 1000
		}
		batches = append(batches, &batch.Batch{
			Length: size,
			Schema: []batch.ColumnMeta{{Name: "dur", Origin: "t"}},
			Columns: []batch.ColumnVector{
				&batch.Float64Vector{Values: vals},
			},
		})
		produced += size
	}
	return batches
}

// genUserIDs returns n unique user ID strings.
func genUserIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("user-%06d", i)
	}
	return ids
}

// ---- helpers ----------------------------------------------------------------

func countAggItem() expr.CompiledAggItem {
	return expr.CompileAggItem(
		ast.AggItem{Alias: "n", Call: ast.AggCall{Func: ast.AggCount}},
		source.TypeInt64,
	)
}

func avgAggItem(field string) expr.CompiledAggItem {
	f := ast.FieldRef{Name: field}
	return expr.CompileAggItem(
		ast.AggItem{Alias: "avg_val", Call: ast.AggCall{Func: ast.AggAvg, Field: &f}},
		source.TypeFloat64,
	)
}

func stringGroupBy(colName string) GroupByCol {
	return GroupByCol{
		Name:   colName,
		Origin: "t",
		Type:   source.TypeString,
		Eval:   expr.CompileScalar(&ast.FieldRefExpr{Ref: ast.FieldRef{Name: colName}}),
	}
}

func drainOp(b *testing.B, op Operator) {
	b.Helper()
	for {
		batch, err := op.Next()
		if err != nil {
			break
		}
		_ = batch
	}
}

// ---- BenchmarkHashAgg_HighCardinality ---------------------------------------
// 100k rows, 10k unique string keys — exercises fmt.Sprintf key hashing.

func BenchmarkHashAgg_HighCardinality(b *testing.B) {
	rng := rand.New(rand.NewPCG(benchSeed, 0))
	userIDs := genUserIDs(10_000)
	batches := genStringInt32Batches(100_000, 512, userIDs, rng)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		op := &HashAggregateOp{
			Input:     &fakeOperator{batches: batches},
			Aggs:      []expr.CompiledAggItem{countAggItem()},
			GroupBy:   []GroupByCol{stringGroupBy("key")},
			BatchSize: 512,
		}
		drainOp(b, op)
	}
}

// ---- BenchmarkHashAgg_LowCardinality ----------------------------------------
// 100k rows, 5 unique string keys — exercises accumulator hot path.

func BenchmarkHashAgg_LowCardinality(b *testing.B) {
	rng := rand.New(rand.NewPCG(benchSeed, 0))
	batches := genStringInt32Batches(100_000, 512, regions, rng)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		op := &HashAggregateOp{
			Input:     &fakeOperator{batches: batches},
			Aggs:      []expr.CompiledAggItem{countAggItem()},
			GroupBy:   []GroupByCol{stringGroupBy("key")},
			BatchSize: 512,
		}
		drainOp(b, op)
	}
}

// ---- BenchmarkSort_100k -----------------------------------------------------
// 100k rows, one float64 sort key — exercises materializedRows accumulation
// and compareAny in the sort pass.

func BenchmarkSort_100k(b *testing.B) {
	rng := rand.New(rand.NewPCG(benchSeed, 0))
	batches := genFloat64Batches(100_000, 512, rng)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		op := &SortOp{
			Input:     &fakeOperator{batches: batches},
			Keys:      []CompiledSortKey{{ColIdx: 0, Desc: false}},
			BatchSize: 512,
		}
		drainOp(b, op)
	}
}

// ---- BenchmarkHashJoin_10k_x_50 --------------------------------------------
// Left: 10k rows (userId string, statusCode int32).
// Right: 50 rows (userId string, tier string).
// Join key: userId (col 0 on both sides).
// Exercises build-phase [][]any allocation and probe scan.

func BenchmarkHashJoin_10k_x_50(b *testing.B) {
	rng := rand.New(rand.NewPCG(benchSeed, 0))
	allUserIDs := genUserIDs(50)
	leftBatches := genStringInt32Batches(10_000, 512, allUserIDs, rng)

	// Right side: 50 rows, one per user, with a tier string column.
	tiers := []string{"free", "pro", "enterprise"}
	rightKeys := make([]string, 50)
	rightTiers := make([]string, 50)
	for i := range rightKeys {
		rightKeys[i] = allUserIDs[i]
		rightTiers[i] = tiers[i%len(tiers)]
	}
	rightBatch := &batch.Batch{
		Length: 50,
		Schema: []batch.ColumnMeta{
			{Name: "userId", Origin: "users"},
			{Name: "tier", Origin: "users"},
		},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: rightKeys},
			&batch.StringVector{Values: rightTiers},
		},
	}
	rightBatches := []*batch.Batch{rightBatch}

	leftSchema := []batch.ColumnMeta{
		{Name: "key", Origin: "t"},
		{Name: "value", Origin: "t"},
	}
	rightSchema := rightBatch.Schema

	outSchema := append(leftSchema, rightSchema...)
	outKinds := []VecKind{vecString, vecInt32, vecString, vecString}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		op := &HashJoinOp{
			Left:         &fakeOperator{batches: leftBatches},
			Right:        &fakeOperator{batches: rightBatches},
			LeftKeyIdxs:  []int{0},
			RightKeyIdxs: []int{0},
			OutSchema:    outSchema,
			OutKinds:     outKinds,
			BatchSize:    512,
		}
		drainOp(b, op)
	}
}
