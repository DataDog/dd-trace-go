package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type PackageStats struct {
	Package string
	Pass    int
	Fail    int
}

type TestJob struct {
	Package   string
	Iteration int
}

func main() {
	iterations := flag.Int("n", 1, "number of times to test each package")
	parallelism := flag.Int("p", runtime.NumCPU(), "number of parallel test workers")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-n iterations] [-p parallelism] <csv-file>\n", os.Args[0])
		os.Exit(1)
	}

	csvFile := flag.Arg(0)

	// Read existing CSV data
	stats, err := readCSV(csvFile)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error reading CSV: %v\n", err)
		os.Exit(1)
	}

	// Get list of packages to test
	packages, err := getPackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting packages: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Testing %d packages with %d workers (%d iterations per package)\n",
		len(packages), *parallelism, *iterations)

	// Create jobs channel
	jobs := make(chan TestJob, len(packages)**iterations)

	// Mutex to protect stats map
	var statsMutex sync.Mutex

	// WaitGroup to wait for all workers
	var wg sync.WaitGroup

	// Start workers
	for w := 0; w < *parallelism; w++ {
		wg.Add(1)
		go worker(w, jobs, stats, &statsMutex, &wg)
	}

	// Send jobs
	for _, pkg := range packages {
		for i := 0; i < *iterations; i++ {
			jobs <- TestJob{
				Package:   pkg,
				Iteration: i + 1,
			}
		}
	}
	close(jobs)

	// Wait for all workers to finish
	wg.Wait()

	// Print summary
	fmt.Println("\nSummary:")
	packages = sortPackages(stats)
	for _, pkg := range packages {
		stat := stats[pkg]
		fmt.Printf("  %s: %d pass, %d fail\n", stat.Package, stat.Pass, stat.Fail)
	}

	// Write updated CSV
	if err := writeCSV(csvFile, stats); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing CSV: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nResults written to %s\n", csvFile)
}

func worker(id int, jobs <-chan TestJob, stats map[string]*PackageStats, mu *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()

	for job := range jobs {
		fmt.Printf("[worker %d] Testing %s (iteration %d)...\n", id, job.Package, job.Iteration)
		passed := runTest(job.Package)

		// Update stats (thread-safe)
		mu.Lock()
		if _, exists := stats[job.Package]; !exists {
			stats[job.Package] = &PackageStats{Package: job.Package}
		}
		if passed {
			stats[job.Package].Pass++
			fmt.Printf("[worker %d] %s (iteration %d): PASS\n", id, job.Package, job.Iteration)
		} else {
			stats[job.Package].Fail++
			fmt.Printf("[worker %d] %s (iteration %d): FAIL\n", id, job.Package, job.Iteration)
		}
		mu.Unlock()
	}
}

func sortPackages(stats map[string]*PackageStats) []string {
	packages := make([]string, 0, len(stats))
	for pkg := range stats {
		packages = append(packages, pkg)
	}

	// Sort packages for consistent output
	for i := 0; i < len(packages); i++ {
		for j := i + 1; j < len(packages); j++ {
			if packages[i] > packages[j] {
				packages[i], packages[j] = packages[j], packages[i]
			}
		}
	}

	return packages
}

func readCSV(filename string) (map[string]*PackageStats, error) {
	stats := make(map[string]*PackageStats)

	file, err := os.Open(filename)
	if err != nil {
		return stats, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	// Skip header row if present
	startIdx := 0
	if len(records) > 0 && records[0][0] == "package" {
		startIdx = 1
	}

	for _, record := range records[startIdx:] {
		if len(record) != 3 {
			continue
		}

		pass, err := strconv.Atoi(record[1])
		if err != nil {
			continue
		}

		fail, err := strconv.Atoi(record[2])
		if err != nil {
			continue
		}

		stats[record[0]] = &PackageStats{
			Package: record[0],
			Pass:    pass,
			Fail:    fail,
		}
	}

	return stats, nil
}

func writeCSV(filename string, stats map[string]*PackageStats) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	if err := writer.Write([]string{"package", "pass", "fail"}); err != nil {
		return err
	}

	// Write data rows (sorted by package name for consistency)
	packages := sortPackages(stats)

	for _, pkg := range packages {
		stat := stats[pkg]
		row := []string{
			stat.Package,
			strconv.Itoa(stat.Pass),
			strconv.Itoa(stat.Fail),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

func getPackages() ([]string, error) {
	// Run: go list ./... | grep -v /contrib/
	cmd := exec.Command("sh", "-c", "go list ./... | grep -v /contrib/")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	packages := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			packages = append(packages, line)
		}
	}

	return packages, nil
}

func runTest(pkg string) bool {
	cmd := exec.Command("go", "test", "-shuffle", "on", pkg)
	err := cmd.Run()
	return err == nil
}
