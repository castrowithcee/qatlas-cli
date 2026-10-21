package invokelog_test

import (
	"strconv"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

// BenchmarkAppendManyDays measures one Append with many day files in the logs directory: the directory
// read and sort per Append grow with the file count. "empty-newest" has an empty newest file. The fixture
// and the measured step are those of `qatlas selftest storage` (internal/selftest). The package is external
// because selftest imports invokelog.
//
//	go test ./internal/invokelog -run '^$' -bench BenchmarkAppendManyDays -benchtime 200x -count 3
//
// As a prebuilt test executable:
//
//	invokelog.test.exe -test.run '^$' -test.bench BenchmarkAppendManyDays -test.benchtime 200x -test.count 3
func BenchmarkAppendManyDays(b *testing.B) {
	for _, emptyNewest := range []bool{false, true} {
		for _, days := range []int{1, 90, 365, 3650} {
			name := "days-" + strconv.Itoa(days)
			if emptyNewest {
				name += "-empty-newest"
			}
			b.Run(name, func(b *testing.B) {
				fx, err := selftest.NewAppendFixture(b.TempDir(), days, emptyNewest)
				if err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := fx.Step(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
