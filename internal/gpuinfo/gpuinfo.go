package gpuinfo

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DmonMetrics struct {
	Id    int `json:"id"`
	Pwr   int `json:"pwr"`
	Gtemp int `json:"gtemp"`
	Mtemp int `json:"mtemp"`
	Sm    int `json:"sm"`
	Mem   int `json:"mem"`
	Enc   int `json:"enc"`
	Dec   int `json:"dec"`
	Jpg   int `json:"jpg"`
	Ofa   int `json:"ofa"`
	Mclk  int `json:"mclk"`
	Pclk  int `json:"pclk"`
	Pviol int `json:"pviol"`
	Tviol int `json:"tviol"`
	Fb    int `json:"fb"`
	Bar1  int `json:"bar1"`
	Ccpm  int `json:"ccpm"`
	Sbecc int `json:"sbecc"`
	Dbecc int `json:"dbecc"`
	Pci   int `json:"pci"`
	Rxpci int `json:"rxpci"`
	Txpci int `json:"txpci"`
	Gpu   GPU `json:"gpu"`
}

type QueryMetrics struct {
	UtilGpu int    `json:"utilgpu"`
	MemUsed int    `json:"memused"`
	MemFree int    `json:"memfree"`
	DrivVer string `json:"drivver"`
	FanSpe  int    `json:"fanspe"`
	Pstat   string `json:"pstat"`
}

type GpuState struct {
	Gpu          GPU          `json:"gpu"`
	DmonMetrics  DmonMetrics  `json:"dmon"`
	QueryMetrics QueryMetrics `json:"query"`
}

type GPU struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Uuid  string `json:"uuid"`
}

// GetGpuInfo extracts a list of all GPUs found by nividia-smi package
func GetGpuInfo() ([]GPU, error) {
	cmd := exec.Command("nvidia-smi", "--query-gpu", "index,gpu_name,gpu_uuid", "--format", "csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run nvidia-smi: %w", err)
	}

	var gpus []GPU
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ",")

		if len(parts) != 3 {
			continue
		}

		index, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		gpu := GPU{
			Index: index,
			Name:  strings.TrimSpace(parts[1]),
			Uuid:  strings.TrimSpace(parts[2]),
		}
		gpus = append(gpus, gpu)
	}

	err = scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("error reading nvidia-smi output: %w", err)
	}

	return gpus, nil
}

func CombinedMonitor(ctx context.Context, logger *slog.Logger, gpu GPU) (<-chan GpuState, error) {
	// outside faceing channel
	combinedStateChan := make(chan GpuState)

	// internal channels
	dmonChan := make(chan DmonMetrics)
	queryChan := make(chan QueryMetrics)

	var wg sync.WaitGroup

	// Goroutine for dmon
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(dmonChan)
		runDmon(ctx, logger, gpu, dmonChan)
	}()

	// Goroutine for query
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(queryChan)
		runQuery(ctx, logger, gpu, 1*time.Second, queryChan)
	}()

	// The "Merger"-goroutine, to combine both
	go func() {
		defer close(combinedStateChan)

		workersDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(workersDone)
		}()

		var currentState GpuState
		currentState.Gpu = gpu

		sendUpdatedState := func() {
			select {
			case combinedStateChan <- currentState:
			case <-ctx.Done():
			}
		}

		for {
			select {
			case <-ctx.Done():
				logger.Info("combined monitor context cancelled, shutting down", "gpu_uuid", gpu.Uuid)
				return

			case <-workersDone:
				logger.Info("all workers finished, shutting down combined monitor", "gpu_uuid", gpu.Uuid)
				return

			case dmonData, ok := <-dmonChan:
				if !ok {
					dmonChan = nil
					logger.Debug("dmon channel closed", "gpu_uuid", gpu.Uuid)
				} else {
					currentState.DmonMetrics = dmonData
					sendUpdatedState()
				}

			case queryData, ok := <-queryChan:
				if !ok {
					queryChan = nil
					logger.Debug("query channel closed", "gpu_uuid", gpu.Uuid)
				} else {
					currentState.QueryMetrics = queryData
					sendUpdatedState()
				}
			}

			// If both channels are closed
			if dmonChan == nil && queryChan == nil {
				logger.Info("all channels closed, shutting down combined monitor", "gpu_uuid", gpu.Uuid)
				return
			}
		}
	}()

	return combinedStateChan, nil
}

func runQuery(ctx context.Context, logger *slog.Logger, gpu GPU, interval time.Duration, out chan<- QueryMetrics) {
	if !isValidGPUUUID(gpu.Uuid) {
		logger.Error("invalid GPU UUID format", "gpu_uuid", gpu.Uuid)
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=utilization.gpu,memory.used,memory.free,driver_version,fan.speed,pstate", "--format=csv,noheader,nounits", "-i", gpu.Uuid)

			output, err := cmd.Output()
			if err != nil {
				logger.Error("failed to run query-gpu", "gpu_uuid", gpu.Uuid, "error", err)
				continue
			}

			metrics := parseQueryLine(string(output))
			out <- metrics
		}
	}
}

func runDmon(ctx context.Context, logger *slog.Logger, gpu GPU, out chan<- DmonMetrics) {
	if !isValidGPUUUID(gpu.Uuid) {
		logger.Error("invalid GPU UUID format", "gpu_uuid", gpu.Uuid)
		return
	}
	cmd := exec.CommandContext(ctx, "nvidia-smi", "dmon", "-s", "pucvmet", "--format", "csv,noheader,nounit", "-i", gpu.Uuid)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("failed to create dmon stdout pipe", "gpu_uuid", gpu.Uuid, "error", err)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		logger.Error("failed to create dmon stderr pipe", "gpu_uuid", gpu.Uuid, "error", err)
		return
	}

	err = cmd.Start()
	if err != nil {
		logger.Error("failed to start dmon", "gpu_uuid", gpu.Uuid, "error", err)
		return
	}

	defer cmd.Wait()
	// Goroutine for error readout
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logger.Error("dmon process error", "gpu_uuid", gpu.Uuid, "error", scanner.Text())
		}
	}()

	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	for line := range lines {
		metrics := parseDmonLine(line)

		select {
		case out <- metrics:
		case <-ctx.Done():
			logger.Info("dmon context cancelled during send, shutting down monitor", "gpu_uuid", gpu.Uuid)
			return
		}
	}
	logger.Info("dmon process finished, shutting down monitor", "gpu_uuid", gpu.Uuid)
}

// Sometimes "-" is returned by smi; now it is treated as 0
func parseInt(s string) int {
	val := strings.TrimSpace(s)
	if val == "-" {
		return 0
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return i
}

func parseDmonLine(line string) DmonMetrics {
	parts := strings.Split(line, ",")

	if len(parts) != 22 {
		return DmonMetrics{}
	}

	metrics := DmonMetrics{
		Id:    parseInt(parts[0]),
		Pwr:   parseInt(parts[1]),
		Gtemp: parseInt(parts[2]),
		Mtemp: parseInt(parts[3]),
		Sm:    parseInt(parts[4]),
		Mem:   parseInt(parts[5]),
		Enc:   parseInt(parts[6]),
		Dec:   parseInt(parts[7]),
		Jpg:   parseInt(parts[8]),
		Ofa:   parseInt(parts[9]),
		Mclk:  parseInt(parts[10]),
		Pclk:  parseInt(parts[11]),
		Pviol: parseInt(parts[12]),
		Tviol: parseInt(parts[13]),
		Fb:    parseInt(parts[14]),
		Bar1:  parseInt(parts[15]),
		Ccpm:  parseInt(parts[16]),
		Sbecc: parseInt(parts[17]),
		Dbecc: parseInt(parts[18]),
		Pci:   parseInt(parts[19]),
		Rxpci: parseInt(parts[20]),
		Txpci: parseInt(parts[21]),
	}

	return metrics
}

func parseQueryLine(line string) QueryMetrics {
	parts := strings.Split(strings.TrimSpace(line), ",")
	if len(parts) != 6 {
		return QueryMetrics{}
	}

	return QueryMetrics{
		UtilGpu: parseInt(parts[0]),
		MemUsed: parseInt(parts[1]),
		MemFree: parseInt(parts[2]),
		DrivVer: strings.TrimSpace(parts[3]),
		FanSpe:  parseInt(parts[4]),
		Pstat:   strings.TrimSpace(parts[5]),
	}
}

func isValidGPUUUID(uuid string) bool {
	matched, _ := regexp.MatchString(`^GPU-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`, uuid)
	return matched
}
