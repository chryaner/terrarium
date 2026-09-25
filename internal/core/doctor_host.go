package core

import (
	"os"
	"path/filepath"
	"runtime"
)

// hostChecks covers what VBoxManage answering does not prove: on Linux it runs
// fine with no kernel driver loaded, and only VM start fails.
func hostChecks() []Check {
	if runtime.GOOS != "linux" {
		return nil
	}
	return linuxKernelChecks("/sys/module")
}

// linuxKernelChecks reads loaded modules from a /sys/module layout; tests
// point it at a fake one.
func linuxKernelChecks(modules string) []Check {
	loaded := func(name string) bool {
		_, err := os.Stat(filepath.Join(modules, name))
		return err == nil
	}

	drv := Check{Name: "vboxdrv kernel module", OK: true, Detail: "loaded"}
	if !loaded("vboxdrv") {
		drv = Check{
			Name:   "vboxdrv kernel module",
			Detail: "not loaded, so no VM can start",
			Fix:    "run `sudo /sbin/vboxconfig` to build and load it; with Secure Boot on, the module has to be signed first or the kernel refuses it",
		}
	}
	checks := []Check{drv}

	for _, kvm := range []string{"kvm_intel", "kvm_amd"} {
		if loaded(kvm) {
			checks = append(checks, Check{
				Name:     "KVM",
				Optional: true,
				Detail:   kvm + " is loaded and may hold the CPU's virtualization extensions; VMs then fail to start with VERR_VMX_IN_VMX_ROOT_MODE or VERR_SVM_IN_USE",
				Fix:      "if they do, run `sudo modprobe -r " + kvm + "` while using VirtualBox, or blacklist it",
			})
			break
		}
	}
	return checks
}
