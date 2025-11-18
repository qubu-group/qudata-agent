package utils

import "os"

// GetGPUCountSafe возвращает количество GPU, учитывая режим отладки
func GetGPUCountSafe() int {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return 1 // Мок: 1 GPU
	}
	return GetGPUCount()
}

// GetGPUNameSafe возвращает название GPU, учитывая режим отладки
func GetGPUNameSafe() string {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return "H100" // Мок: H100
	}
	return GetGPUName()
}

// GetVRAMSafe возвращает объем VRAM, учитывая режим отладки
func GetVRAMSafe() float64 {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return 70.0 // Мок: 70 GB VRAM
	}
	return GetVRAM()
}

// GetMaxCUDAVersionSafe возвращает максимальную версию CUDA, учитывая режим отладки
func GetMaxCUDAVersionSafe() float64 {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return 12.2 // Мок: CUDA 12.2
	}
	return GetMaxCUDAVersion()
}

// GetGPUTemperatureSafe возвращает температуру GPU, учитывая режим отладки
func GetGPUTemperatureSafe() int {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return 45 // Мок: 45°C
	}
	return GetGPUTemperature()
}

// GetGPUUtilSafe возвращает утилизацию GPU, учитывая режим отладки
func GetGPUUtilSafe() float64 {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return 0.0 // Мок: 0% утилизация
	}
	return GetGPUUtil()
}

// GetMemUtilSafe возвращает утилизацию памяти GPU, учитывая режим отладки
func GetMemUtilSafe() float64 {
	if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
		return 0.0 // Мок: 0% утилизация памяти
	}
	return GetMemUtil()
}
