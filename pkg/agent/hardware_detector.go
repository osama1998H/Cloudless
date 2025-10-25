package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// HardwareDetector detects hardware capabilities
type HardwareDetector struct {
	logger *zap.Logger
}

// NewHardwareDetector creates a new hardware detector
func NewHardwareDetector(logger *zap.Logger) *HardwareDetector {
	return &HardwareDetector{
		logger: logger,
	}
}

// DetectAcceleratorType detects the type of GPU/accelerator present
// Returns a string like "nvidia-tesla-t4", "nvidia-a100", "amd-mi100", etc.
// Returns empty string if no accelerator is detected
func (hd *HardwareDetector) DetectAcceleratorType() string {
	// Try NVIDIA detection first
	if nvType := hd.detectNVIDIA(); nvType != "" {
		return nvType
	}

	// Try AMD detection
	if amdType := hd.detectAMD(); amdType != "" {
		return amdType
	}

	// Try Intel detection
	if intelType := hd.detectIntel(); intelType != "" {
		return intelType
	}

	hd.logger.Debug("No GPU accelerator detected")
	return ""
}

// detectNVIDIA detects NVIDIA GPUs using nvidia-smi
func (hd *HardwareDetector) detectNVIDIA() string {
	// Try to run nvidia-smi
	cmd := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// nvidia-smi not found or failed
		hd.logger.Debug("nvidia-smi not available", zap.Error(err))
		return ""
	}

	// Parse GPU name
	gpuName := strings.TrimSpace(out.String())
	if gpuName == "" {
		return ""
	}

	hd.logger.Info("Detected NVIDIA GPU", zap.String("gpu_name", gpuName))

	// Map to standardized type names
	gpuNameLower := strings.ToLower(gpuName)

	// Tesla series
	if strings.Contains(gpuNameLower, "tesla t4") {
		return "nvidia-tesla-t4"
	}
	if strings.Contains(gpuNameLower, "tesla v100") {
		return "nvidia-tesla-v100"
	}
	if strings.Contains(gpuNameLower, "tesla p100") {
		return "nvidia-tesla-p100"
	}
	if strings.Contains(gpuNameLower, "tesla p4") {
		return "nvidia-tesla-p4"
	}

	// A-series (Ampere/Hopper)
	if strings.Contains(gpuNameLower, "a100") {
		return "nvidia-a100"
	}
	if strings.Contains(gpuNameLower, "a40") {
		return "nvidia-a40"
	}
	if strings.Contains(gpuNameLower, "a30") {
		return "nvidia-a30"
	}
	if strings.Contains(gpuNameLower, "a10") {
		return "nvidia-a10"
	}
	if strings.Contains(gpuNameLower, "h100") {
		return "nvidia-h100"
	}

	// RTX series
	if strings.Contains(gpuNameLower, "rtx 4090") {
		return "nvidia-rtx-4090"
	}
	if strings.Contains(gpuNameLower, "rtx 4080") {
		return "nvidia-rtx-4080"
	}
	if strings.Contains(gpuNameLower, "rtx 3090") {
		return "nvidia-rtx-3090"
	}
	if strings.Contains(gpuNameLower, "rtx 3080") {
		return "nvidia-rtx-3080"
	}
	if strings.Contains(gpuNameLower, "rtx 3070") {
		return "nvidia-rtx-3070"
	}

	// GTX series
	if strings.Contains(gpuNameLower, "gtx 1080") {
		return "nvidia-gtx-1080"
	}
	if strings.Contains(gpuNameLower, "gtx 1070") {
		return "nvidia-gtx-1070"
	}

	// Generic NVIDIA if we can't identify specific model
	return fmt.Sprintf("nvidia-%s", strings.ReplaceAll(gpuNameLower, " ", "-"))
}

// detectAMD detects AMD GPUs using rocm-smi
func (hd *HardwareDetector) detectAMD() string {
	// Try to run rocm-smi
	cmd := exec.Command("rocm-smi", "--showproductname")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// rocm-smi not found or failed
		hd.logger.Debug("rocm-smi not available", zap.Error(err))
		return ""
	}

	// Parse output
	output := out.String()

	// Look for GPU product name
	re := regexp.MustCompile(`(?i)card\d+:\s+(.+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}

	gpuName := strings.TrimSpace(matches[1])
	hd.logger.Info("Detected AMD GPU", zap.String("gpu_name", gpuName))

	gpuNameLower := strings.ToLower(gpuName)

	// MI series (data center)
	if strings.Contains(gpuNameLower, "mi250") {
		return "amd-mi250"
	}
	if strings.Contains(gpuNameLower, "mi210") {
		return "amd-mi210"
	}
	if strings.Contains(gpuNameLower, "mi100") {
		return "amd-mi100"
	}
	if strings.Contains(gpuNameLower, "mi60") {
		return "amd-mi60"
	}
	if strings.Contains(gpuNameLower, "mi50") {
		return "amd-mi50"
	}

	// Radeon series
	if strings.Contains(gpuNameLower, "radeon vii") {
		return "amd-radeon-vii"
	}
	if strings.Contains(gpuNameLower, "radeon rx 7900") {
		return "amd-radeon-rx-7900"
	}
	if strings.Contains(gpuNameLower, "radeon rx 6900") {
		return "amd-radeon-rx-6900"
	}

	// Generic AMD if we can't identify specific model
	return fmt.Sprintf("amd-%s", strings.ReplaceAll(gpuNameLower, " ", "-"))
}

// detectIntel detects Intel GPUs/accelerators
func (hd *HardwareDetector) detectIntel() string {
	// Try lspci to detect Intel GPUs
	cmd := exec.Command("lspci")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		hd.logger.Debug("lspci not available", zap.Error(err))
		return ""
	}

	output := out.String()
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		lineLower := strings.ToLower(line)

		// Check for Intel GPU
		if !strings.Contains(lineLower, "intel") {
			continue
		}

		// Data Center GPU Max series
		if strings.Contains(lineLower, "data center gpu max") {
			if strings.Contains(lineLower, "1550") {
				return "intel-max-1550"
			}
			if strings.Contains(lineLower, "1100") {
				return "intel-max-1100"
			}
			return "intel-max"
		}

		// Ponte Vecchio
		if strings.Contains(lineLower, "ponte vecchio") {
			return "intel-ponte-vecchio"
		}

		// Arc series
		if strings.Contains(lineLower, "arc a770") {
			return "intel-arc-a770"
		}
		if strings.Contains(lineLower, "arc a750") {
			return "intel-arc-a750"
		}
		if strings.Contains(lineLower, "arc") {
			return "intel-arc"
		}
	}

	return ""
}
