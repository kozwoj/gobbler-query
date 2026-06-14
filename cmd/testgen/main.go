// cmd/testgen generates committed static test data for source-layer and
// query-execution tests. Output matches the file layout that Gobbler produces:
// one {typeName}.json per type directory and CSV files named after the timestamp of
// their first row.
//
// Run: go run ./cmd/testgen -seed 42 -out ./testdata
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// ---- file-format constants ---------------------------------------------------

const (
	datetimeFmt    = "2006-01-02 15:04:05.000"
	filenameFmt    = "2006-01-02_15-04-05.000"
	windowDuration = 12 * time.Hour // one file per 12-hour window (720 min latency)
	numUsers       = 50
)

// refStart is the simulated start of the request data (first file).
var refStart = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

// usersWindowStart is the start of the users ingest window (day before requests).
var usersWindowStart = time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

// ---- {typeName}.json types (matches Gobbler's storedItemDef) ----------------------

type storedColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type storedItemDef struct {
	Name           string         `json:"name"`
	OrderedColumns []storedColumn `json:"orderedColumns"`
}

var requestsTypeDef = storedItemDef{
	Name: "requests",
	OrderedColumns: []storedColumn{
		{Name: "timestamp", Type: "datetime"},
		{Name: "requestId", Type: "string"},
		{Name: "userId", Type: "string"},
		{Name: "requestCode", Type: "string"},
		{Name: "statusCode", Type: "int"},
		{Name: "durationMs", Type: "real"},
		{Name: "region", Type: "string"},
		{Name: "ttl", Type: "timespan"},
	},
}

var usersTypeDef = storedItemDef{
	Name: "users",
	OrderedColumns: []storedColumn{
		{Name: "timestamp", Type: "datetime"},
		{Name: "userId", Type: "string"},
		{Name: "tier", Type: "string"},
		{Name: "active", Type: "bool"},
		{Name: "countryCode", Type: "string"},
		{Name: "signupDate", Type: "datetime"},
	},
}

// ---- domain tables -----------------------------------------------------------

var requestCodes = []string{
	"login", "tokenvalidation", "userinfo", "tokenexchange", "logout", "signup",
}
var requestCodeWeights = []int{30, 25, 18, 12, 8, 7}

var statusCodes = []int{200, 201, 400, 401, 500}

// statusWeights[requestCodeIdx][statusCodeIdx]: correlated per-endpoint weights.
var statusWeights = [][]int{
	// login           200  201  400  401  500
	{50, 0, 10, 30, 10},
	// tokenvalidation 200  201  400  401  500
	{75, 0, 0, 15, 10},
	// userinfo        200  201  400  401  500
	{70, 0, 10, 15, 5},
	// tokenexchange   200  201  400  401  500
	{55, 0, 10, 25, 10},
	// logout          200  201  400  401  500
	{90, 0, 5, 0, 5},
	// signup          200  201  400  401  500
	{10, 50, 30, 0, 10},
}

// baseDurationMs[requestCodeIdx]: median response time in ms per endpoint.
var baseDurationMs = []float64{40, 8, 25, 60, 15, 80}

// durationMultiplier[statusCodeIdx]: response-time factor per status outcome.
var durationMultiplier = []float64{
	1.0, // 200
	1.0, // 201
	0.5, // 400 — fast rejection
	0.8, // 401 — auth check done quickly
	8.0, // 500 — server error / timeout
}

var regions = []string{"eastus", "westus", "northeurope", "southeastasia"}
var regionWeights = []int{40, 30, 20, 10}

var tiers = []string{"free", "pro", "enterprise"}
var tierWeights = []int{60, 30, 10}

var countryCodes = []string{"US", "GB", "DE", "FR", "CA", "AU", "JP", "BR", "IN", "NL"}

// ttlValues are realistic auth-service token TTLs (Go duration strings).
var ttlValues = []string{"15m", "30m", "1h", "2h", "4h", "8h", "24h"}

// ---- helpers -----------------------------------------------------------------

// pick returns a weighted random index into weights using rng.
func pick(rng *rand.Rand, weights []int) int {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := rng.IntN(total)
	for i, w := range weights {
		n -= w
		if n < 0 {
			return i
		}
	}
	return len(weights) - 1
}

// maybeNull returns "" (null) with probability pctNull/100, otherwise s.
func maybeNull(rng *rand.Rand, s string, pctNull int) string {
	if rng.IntN(100) < pctNull {
		return ""
	}
	return s
}

func formatDatetime(t time.Time) string {
	return t.UTC().Format(datetimeFmt)
}

func csvFilename(firstTS time.Time, typeName string) string {
	return firstTS.UTC().Format(filenameFmt) + "_" + typeName + ".csv"
}

func writeTypeJSON(dir string, def storedItemDef) error {
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, def.Name+".json"), data, 0644)
}

// buildUserIDs returns the deterministic list of userId values.
func buildUserIDs() []string {
	ids := make([]string, numUsers)
	for i := range ids {
		ids[i] = fmt.Sprintf("user-%03d", i+1)
	}
	return ids
}

// ---- users -------------------------------------------------------------------

func generateUsers(rng *rand.Rand, dir string, userIDs []string) error {
	dayDur := 24 * time.Hour
	signupOrigin := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	signupRangeS := int(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).Sub(signupOrigin).Seconds())

	rows := make([][]string, numUsers)
	for i, uid := range userIDs {
		// Spread ingest timestamps across the users window day.
		offS := rng.IntN(int(dayDur.Seconds()))
		offMs := rng.IntN(1000)
		ts := usersWindowStart.Add(time.Duration(offS)*time.Second + time.Duration(offMs)*time.Millisecond)

		tier := tiers[pick(rng, tierWeights)]
		active := "true"
		if rng.IntN(100) < 20 {
			active = "false"
		}
		cc := maybeNull(rng, countryCodes[rng.IntN(len(countryCodes))], 5)

		// signupDate: months before the request data window.
		signupOffS := rng.IntN(signupRangeS)
		signupDate := signupOrigin.Add(time.Duration(signupOffS) * time.Second)

		rows[i] = []string{
			formatDatetime(ts),
			uid,
			tier,
			active,
			cc,
			formatDatetime(signupDate),
		}
	}

	// Sort by ingest timestamp (col 0 — ISO format sorts lexicographically).
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })

	firstTS := usersWindowStart // file is named after the start of the ingest window
	fname := csvFilename(firstTS, "users")
	return writeCSV(filepath.Join(dir, fname), rows)
}

// ---- requests ----------------------------------------------------------------

func generateRequests(rng *rand.Rand, dir string, userIDs []string, files, rowsPerFile int) error {
	reqCounter := 0

	for fi := 0; fi < files; fi++ {
		windowStart := refStart.Add(time.Duration(fi) * windowDuration)

		// Generate rowsPerFile random sub-second offsets within the 12-hour window,
		// then sort them so rows are in ascending timestamp order.
		type offset struct{ s, ms int }
		offsets := make([]offset, rowsPerFile)
		windowS := int(windowDuration.Seconds())
		for i := range offsets {
			offsets[i] = offset{s: rng.IntN(windowS), ms: rng.IntN(1000)}
		}
		sort.Slice(offsets, func(i, j int) bool {
			if offsets[i].s != offsets[j].s {
				return offsets[i].s < offsets[j].s
			}
			return offsets[i].ms < offsets[j].ms
		})

		rows := make([][]string, rowsPerFile)
		for i, off := range offsets {
			ts := windowStart.Add(
				time.Duration(off.s)*time.Second +
					time.Duration(off.ms)*time.Millisecond,
			)

			reqCounter++
			reqID := fmt.Sprintf("req-%07d", reqCounter)

			// userId optional (5% null — e.g. unauthenticated signup attempt)
			userID := maybeNull(rng, userIDs[rng.IntN(numUsers)], 5)

			// requestCode (weighted)
			rcIdx := pick(rng, requestCodeWeights)
			rc := requestCodes[rcIdx]

			// statusCode (weighted, correlated with requestCode)
			scIdx := pick(rng, statusWeights[rcIdx])
			sc := statusCodes[scIdx]

			// durationMs: base × status-multiplier × uniform jitter [0.5, 1.5)
			base := baseDurationMs[rcIdx] * durationMultiplier[scIdx]
			dur := base * (0.5 + rng.Float64())
			durStr := strconv.FormatFloat(dur, 'f', 3, 64)

			// region optional (3% null)
			region := maybeNull(rng, regions[pick(rng, regionWeights)], 3)

			// ttl optional (10% null)
			ttl := maybeNull(rng, ttlValues[rng.IntN(len(ttlValues))], 10)

			rows[i] = []string{
				formatDatetime(ts),
				reqID,
				userID,
				rc,
				strconv.Itoa(sc),
				durStr,
				region,
				ttl,
			}
		}

		// File named after actual timestamp of first row.
		firstTS := windowStart.Add(
			time.Duration(offsets[0].s)*time.Second +
				time.Duration(offsets[0].ms)*time.Millisecond,
		)
		fname := csvFilename(firstTS, "requests")
		if err := writeCSV(filepath.Join(dir, fname), rows); err != nil {
			return fmt.Errorf("file %s: %w", fname, err)
		}
		fmt.Printf("  %s  (%d rows)\n", fname, rowsPerFile)
	}
	return nil
}

// ---- CSV output --------------------------------------------------------------

func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

// ---- main --------------------------------------------------------------------

func main() {
	seed := flag.Int("seed", 42, "random seed for deterministic generation")
	out := flag.String("out", "./testdata", "output root directory")
	rows := flag.Int("rows", 500, "rows per request file")
	files := flag.Int("files", 14, "total request files (default 14 = 7 days × 2 files/day)")
	flag.Parse()

	rng := rand.New(rand.NewPCG(uint64(*seed), 0))

	reqDir := filepath.Join(*out, "requests")
	usrDir := filepath.Join(*out, "users")
	for _, dir := range []string{reqDir, usrDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error: mkdir %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	if err := writeTypeJSON(reqDir, requestsTypeDef); err != nil {
		fmt.Fprintf(os.Stderr, "error: requests requests.json: %v\n", err)
		os.Exit(1)
	}
	if err := writeTypeJSON(usrDir, usersTypeDef); err != nil {
		fmt.Fprintf(os.Stderr, "error: users users.json: %v\n", err)
		os.Exit(1)
	}

	userIDs := buildUserIDs()

	fmt.Println("generating users...")
	if err := generateUsers(rng, usrDir, userIDs); err != nil {
		fmt.Fprintf(os.Stderr, "error: generate users: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  users CSV (%d rows)\n", numUsers)

	fmt.Println("generating requests...")
	if err := generateRequests(rng, reqDir, userIDs, *files, *rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: generate requests: %v\n", err)
		os.Exit(1)
	}

	totalRows := *files * *rows
	fmt.Printf("done: %d request files × %d rows = %d rows, seed=%d → %s\n",
		*files, *rows, totalRows, *seed, *out)
}
