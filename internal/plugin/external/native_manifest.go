package external

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"runtime"
)

func validateNative(path string) error {
	invalid := errors.New("plugin: native entrypoint does not match the target OS/architecture or is not a dynamic library")
	switch runtime.GOOS {
	case "darwin":
		cpu := macho.CpuAmd64
		if runtime.GOARCH == "arm64" {
			cpu = macho.CpuArm64
		}
		if fat, err := macho.OpenFat(path); err == nil {
			defer fat.Close()
			for _, arch := range fat.Arches {
				if arch.Cpu == cpu && arch.Type == macho.TypeDylib {
					return nil
				}
			}
			return invalid
		}
		file, err := macho.Open(path)
		if err != nil {
			return invalid
		}
		defer file.Close()
		if file.Cpu != cpu || file.Type != macho.TypeDylib {
			return invalid
		}
	case "linux":
		file, err := elf.Open(path)
		if err != nil {
			return invalid
		}
		defer file.Close()
		machine := elf.EM_X86_64
		if runtime.GOARCH == "arm64" {
			machine = elf.EM_AARCH64
		}
		if file.Machine != machine || file.Type != elf.ET_DYN {
			return invalid
		}
	case "windows":
		file, err := pe.Open(path)
		if err != nil {
			return invalid
		}
		defer file.Close()
		machine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
		if runtime.GOARCH == "arm64" {
			machine = pe.IMAGE_FILE_MACHINE_ARM64
		}
		if file.Machine != machine || file.Characteristics&pe.IMAGE_FILE_DLL == 0 {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}
