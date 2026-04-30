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
	"time"
)

// nvidia-smi --query-gpu=index,gpu_name,gpu_uuid --format=csv,noheader,nounits
// nvidia-smi --query-gpu=driver_version --format=csv,noheader
// nvidia-smi --query-gpu=pstate --format=csv,noheader
// nvidia-smi dmon --format csv -s pucvmet
// nvidia-smi dmon --format csv,noheader,nounit -s pucvmet
// nvidia-smi --query-gpu=utilization.gpu,memory.used,memory.free,driver_version,fan.speed,pstate --format=csv,noheader,nounits
// --query-compute-apps=name,used_memory

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

// GetGpuInfo extracts a list of all GPUs found by nvidia-smi.
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

func CombinedMonitor(ctx context.Context, logger *slog.Logger, gpu GPU, dmonIntervalSeconds int, queryInterval time.Duration) (<-chan GpuState, error) {
	// Outgoing channel exposed to callers.
	combinedStateChan := make(chan GpuState)

	// Internal channels for worker results.
	dmonChan := make(chan DmonMetrics)
	queryChan := make(chan QueryMetrics)

	// Goroutine for dmon
	go func() {
		defer close(dmonChan)
		runDmon(ctx, logger, gpu, dmonIntervalSeconds, dmonChan)
	}()

	// Goroutine for query
	go func() {
		defer close(queryChan)
		runQuery(ctx, logger, gpu, queryInterval, queryChan)
	}()

	// Merge worker updates into a single state stream.
	go func() {
		defer close(combinedStateChan)

		var currentState GpuState
		currentState.Gpu = gpu
		channelsOpen := func() bool {
			return dmonChan != nil || queryChan != nil
		}

		// sendUpdatedState helps avoid blocking during shutdown.
		sendUpdatedState := func() {
			select {
			case combinedStateChan <- currentState:
			case <-ctx.Done():
			}
		}
		handleDmon := func(dmonData DmonMetrics, ok bool) {
			if !ok {
				dmonChan = nil
				logger.Debug("dmon channel closed", "gpu_uuid", gpu.Uuid)
				return
			}

			currentState.DmonMetrics = dmonData
			sendUpdatedState()
		}
		handleQuery := func(queryData QueryMetrics, ok bool) {
			if !ok {
				queryChan = nil
				logger.Debug("query channel closed", "gpu_uuid", gpu.Uuid)
				return
			}

			currentState.QueryMetrics = queryData
			sendUpdatedState()
		}

		for {
			if !channelsOpen() {
				logger.Info("all channels closed, shutting down combined monitor", "gpu_uuid", gpu.Uuid)
				return
			}

			select {
			case <-ctx.Done():
				logger.Info("combined monitor context cancelled, shutting down", "gpu_uuid", gpu.Uuid)
				return

			case dmonData, ok := <-dmonChan:
				handleDmon(dmonData, ok)

			case queryData, ok := <-queryChan:
				handleQuery(queryData, ok)
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
	const queryFields = "utilization.gpu,memory.used,memory.free,driver_version,fan.speed,pstate"
	sendMetrics := func(metrics QueryMetrics) bool {
		select {
		case out <- metrics:
			return true
		case <-ctx.Done():
			logger.Info("query context cancelled during send, shutting down monitor", "gpu_uuid", gpu.Uuid)
			return false
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cmd := exec.CommandContext(
				ctx,
				"nvidia-smi",
				"--query-gpu="+queryFields,
				"--format=csv,noheader,nounits",
				"-i",
				gpu.Uuid,
			)

			output, err := cmd.Output()
			if err != nil {
				logger.Error("failed to run query-gpu", "gpu_uuid", gpu.Uuid, "error", err)
				continue
			}

			metrics := parseQueryLine(string(output))
			if !sendMetrics(metrics) {
				return
			}
		}
	}
}

func runDmon(ctx context.Context, logger *slog.Logger, gpu GPU, intervalSeconds int, out chan<- DmonMetrics) {
	if !isValidGPUUUID(gpu.Uuid) {
		logger.Error("invalid GPU UUID format", "gpu_uuid", gpu.Uuid)
		return
	}
	intervalStr := strconv.Itoa(intervalSeconds)
	cmd := exec.CommandContext(ctx, "nvidia-smi", "dmon", "-d", intervalStr, "-s", "pucvmet", "--format", "csv,noheader,nounit", "-i", gpu.Uuid)

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

	defer func() {
		if waitErr := cmd.Wait(); waitErr != nil && ctx.Err() == nil {
			logger.Error("dmon process exited with error", "gpu_uuid", gpu.Uuid, "error", waitErr)
		}
	}()
	// Goroutine for error readout
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logger.Error("dmon process error", "gpu_uuid", gpu.Uuid, "error", scanner.Text())
		}
		if scanErr := scanner.Err(); scanErr != nil && ctx.Err() == nil {
			logger.Error("failed to read dmon stderr", "gpu_uuid", gpu.Uuid, "error", scanErr)
		}
	}()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		metrics := parseDmonLine(line)

		select {
		case out <- metrics:
		case <-ctx.Done():
			logger.Info("dmon context cancelled during send, shutting down monitor", "gpu_uuid", gpu.Uuid)
			return
		}
	}

	if scanErr := scanner.Err(); scanErr != nil && ctx.Err() == nil {
		logger.Error("failed to read dmon stdout", "gpu_uuid", gpu.Uuid, "error", scanErr)
	}
	logger.Info("dmon process finished, shutting down monitor", "gpu_uuid", gpu.Uuid)
}

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
