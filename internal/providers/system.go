package providers

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
)

type cpuSample struct {
	total uint64
	idle  uint64
}

type SystemProvider struct {
	mu       sync.Mutex
	previous cpuSample
	readFile func(string) ([]byte, error)
	hostname func() (string, error)
	disk     func(string) (int, error)
	diskPath string
}

func NewSystemProvider() *SystemProvider {
	return &SystemProvider{
		readFile: os.ReadFile,
		hostname: os.Hostname,
		disk:     diskPercent,
		diskPath: "/",
	}
}

func (p *SystemProvider) Name() string { return "system" }

func (p *SystemProvider) Data(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hostname, err := p.hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}
	stat, err := p.readFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	current, err := parseCPUSample(stat)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	cpu := cpuPercent(p.previous, current)
	p.previous = current
	p.mu.Unlock()

	memoryData, err := p.readFile("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	memory, err := parseMemoryPercent(memoryData)
	if err != nil {
		return nil, err
	}
	uptimeData, err := p.readFile("/proc/uptime")
	if err != nil {
		return nil, fmt.Errorf("uptime: %w", err)
	}
	uptime, err := parseUptime(uptimeData)
	if err != nil {
		return nil, err
	}
	disk, err := p.disk(p.diskPath)
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}
	return map[string]any{
		"system": map[string]any{
			"hostname": hostname,
			"cpu":      cpu,
			"memory":   memory,
			"disk":     disk,
			"uptime":   uptime,
		},
	}, nil
}

func parseCPUSample(data []byte) (cpuSample, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("cpu: invalid /proc/stat")
	}
	var sample cpuSample
	for index, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("cpu: invalid counter: %w", err)
		}
		sample.total += value
		if index == 3 || index == 4 {
			sample.idle += value
		}
	}
	if sample.total == 0 {
		return cpuSample{}, fmt.Errorf("cpu: total counter is zero")
	}
	return sample, nil
}

func cpuPercent(previous, current cpuSample) int {
	total, idle := current.total, current.idle
	if previous.total > 0 && current.total >= previous.total && current.idle >= previous.idle {
		total -= previous.total
		idle -= previous.idle
	}
	if total == 0 || idle > total {
		return 0
	}
	return clampPercent(int(math.Round(float64(total-idle) * 100 / float64(total))))
}

func parseMemoryPercent(data []byte) (int, error) {
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ":")
		if name != "MemTotal" && name != "MemAvailable" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("memory: invalid %s: %w", name, err)
		}
		values[name] = value
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("memory: scan: %w", err)
	}
	total, available := values["MemTotal"], values["MemAvailable"]
	if total == 0 || available > total {
		return 0, fmt.Errorf("memory: missing or invalid MemTotal/MemAvailable")
	}
	return clampPercent(int(math.Round(float64(total-available) * 100 / float64(total)))), nil
}

func parseUptime(data []byte) (int64, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("uptime: invalid /proc/uptime")
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("uptime: invalid seconds")
	}
	return int64(value), nil
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
