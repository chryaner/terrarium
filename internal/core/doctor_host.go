package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// hostChecks covers what VBoxManage answering does not prove: on Linux it runs
// fine with no kernel driver loaded, and only VM start fails.
func hostChecks() []Check {
	switch runtime.GOOS {
	case "linux":
		return append(cpuVirtChecks("/proc/cpuinfo"), linuxKernelChecks("/sys/module")...)
	case "windows":
		return hypervisorChecks(windowsHypervisorPresent())
	}
	return nil
}

// cpuVirtChecks reads the CPU flags for VT-x or AMD-V. VirtualBox 7 has no
// way to run a guest without them, and says so only when the first VM starts
// (VERR_VMX_NO_VMX). A cpuinfo that cannot be read, or has no flags line at
// all, proves nothing either way and is not reported.
func cpuVirtChecks(cpuinfo string) []Check {
	data, err := os.ReadFile(cpuinfo)
	if err != nil {
		return nil
	}
	sawFlags := false
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "flags" {
			continue
		}
		sawFlags = true
		for _, flag := range strings.Fields(value) {
			if flag == "vmx" || flag == "svm" {
				return []Check{{Name: "hardware virtualization", OK: true, Detail: flag}}
			}
		}
	}
	if !sawFlags {
		return nil
	}
	return []Check{{
		Name:   "hardware virtualization",
		Detail: "the CPU reports neither vmx nor svm, so no VM can start",
		Fix:    "turn on VT-x or AMD-V in the firmware setup; if this machine is itself a VM, turn on nested virtualization for it in the hypervisor that runs it",
	}}
}

// windowsHypervisorPresent asks Windows whether its own hypervisor is
// running, which Hyper-V, WSL2, Docker Desktop and virtualization-based
// security all switch on. Unknown counts as no: the answer only adds a
// warning.
func windowsHypervisorPresent() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_ComputerSystem).HypervisorPresent").Output()
	return err == nil && strings.EqualFold(strings.TrimSpace(string(out)), "true")
}

// hypervisorChecks warns that VirtualBox is not the only hypervisor. With
// Windows' own one running, VirtualBox has to run guests through it instead
// of on the CPU directly. Everything still works, which is why this is a
// warning, but it explains what users otherwise meet as unrelated mysteries.
func hypervisorChecks(present bool) []Check {
	if !present {
		return nil
	}
	return []Check{{
		Name:     "Windows hypervisor",
		Optional: true,
		Detail: "Hyper-V is active, so VirtualBox runs guests through it: slower, no nested virtualization, " +
			"and a Linux guest with more than one CPU sometimes hangs in the first seconds of its boot (terrarium resets it and carries on)",
		Fix: "nothing has to be done. For full speed, `bcdedit /set hypervisorlaunchtype off` and a restart turn it off, " +
			"along with everything that needs it: WSL2, Docker Desktop, Windows Sandbox",
	}}
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
			Fix:    "run `sudo modprobe vboxdrv`; if the module is missing, Oracle's package rebuilds it with `sudo /sbin/vboxconfig` and distro packages with their dkms package. With Secure Boot on, the module has to be signed or the kernel refuses it",
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
