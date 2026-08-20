package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseSupportPackages(t *testing.T) {
	// A condensed version of a MathWorks mpm input file: products before the
	// support-package section, explanatory "## ..." comments inside sections,
	// the R2019b stray-underscore artifact, and an OPTIONAL FEATURES section
	// after support packages whose #product. lines must NOT be picked up.
	input := strings.Join([]string{
		"########################################################################",
		"## PRODUCTS",
		"########################################################################",
		"##",
		"## Uncomment the lines for the products you want to install.",
		"",
		"#product.MATLAB",
		"#product.Simulink",
		"",
		"########################################################################",
		"## SUPPORT PACKAGES",
		"########################################################################",
		"##",
		"## Uncomment the lines for the support packages you want to install or download.",
		"",
		"#product.MATLAB_Support_Package_for_Arduino_Hardware",
		"product.Uncommented_Package",
		"#product._GPU_Coder_Support_Package_for_NVIDIA_GPUs_",
		"",
		"########################################################################",
		"## OPTIONAL FEATURES",
		"########################################################################",
		"",
		"#product.Airport_Scene",
		"",
		"########################################################################",
		"## CHECKSUM",
		"########################################################################",
		"",
		"?checksum=UjIwMjZh",
	}, "\n")

	got := parseSupportPackages(strings.NewReader(input))
	want := []string{
		"MATLAB_Support_Package_for_Arduino_Hardware",
		"Uncommented_Package",
		"GPU_Coder_Support_Package_for_NVIDIA_GPUs",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseSupportPackages() = %v, want %v", got, want)
	}
}

func TestSupportPackagesForRelease(t *testing.T) {
	// Support packages first became installable via MPM in R2019a; older
	// releases have no SUPPORT PACKAGES section in their input files.
	for _, release := range []string{"R2017b", "R2018a", "R2018b"} {
		if pkgs := supportPackagesForRelease(release); len(pkgs) != 0 {
			t.Errorf("supportPackagesForRelease(%q) returned %d packages, want 0", release, len(pkgs))
		}
	}

	// Guard: every release from R2019a on must have a vendored input file with
	// a non-empty support-package list. This trips when a new release is added
	// to allReleaseOrder without vendoring its mpm input file.
	for _, release := range allReleaseOrder[releaseIndex("R2019a"):] {
		pkgs := supportPackagesForRelease(release)
		if len(pkgs) < 100 {
			t.Errorf("supportPackagesForRelease(%q) returned only %d packages; missing or truncated file in mpm-input-files?", release, len(pkgs))
		}
	}

	pkgs := supportPackagesForRelease("R2026a")
	if !slices.Contains(pkgs, "MATLAB_Support_Package_for_Arduino_Hardware") {
		t.Error("R2026a support packages missing MATLAB_Support_Package_for_Arduino_Hardware")
	}
	if slices.Contains(pkgs, "MATLAB") || slices.Contains(pkgs, "Airport_Scene") {
		t.Error("R2026a support packages wrongly include entries from the PRODUCTS or OPTIONAL FEATURES sections")
	}
}

func TestRequiresVendorLicense(t *testing.T) {
	requires := []string{
		"Image_Acquisition_Toolbox_Support_Package_for_GenICam_Interface",
		"Image_Acquisition_Toolbox_Support_Package_for_GigE_Vision_Hardware",
		"MATLAB_Support_Package_for_IP_Cameras",
		"MATLAB_Support_Package_for_Parrot_Drones",
		"MATLAB_Support_Package_for_Ryze_Tello_Drones",
		"Simulink_Coder_Support_Package_for_BBC_microbit_Board",
	}
	for _, pkg := range requires {
		if !requiresVendorLicense(pkg) {
			t.Errorf("requiresVendorLicense(%q) = false, want true", pkg)
		}
	}

	noLicense := []string{
		"Deep_Learning_Toolbox_Model_for_ResNet-50_Network",
		"C2000_Microcontroller_Blockset",
		"HDL_Coder_Support_Package_for_Microchip_FPGA_and_SoC_Devices",
	}
	for _, pkg := range noLicense {
		if requiresVendorLicense(pkg) {
			t.Errorf("requiresVendorLicense(%q) = true, want false", pkg)
		}
	}
}

func TestExpandHomePathWith(t *testing.T) {
	home := "/home/tester"
	tests := []struct {
		line string
		want string
	}{
		{"~", "/home/tester"},
		{"~/matlab", "/home/tester/matlab"},
		{`~\matlab`, "/home/tester/matlab"},
		{"~otheruser/matlab", "~otheruser/matlab"},
		{"/opt/matlab", "/opt/matlab"},
		{"", ""},
		{"relative/path", "relative/path"},
	}
	for _, tt := range tests {
		if got := expandHomePathWith(tt.line, home); got != tt.want {
			t.Errorf("expandHomePathWith(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestResolveProducts(t *testing.T) {
	available := []string{"MATLAB", "Simulink", "Wavelet_Toolbox"}

	resolved, unresolved := resolveProducts([]string{"matlab", "SIMULINK"}, available)
	if !slices.Equal(resolved, []string{"MATLAB", "Simulink"}) {
		t.Errorf("resolved = %v, want canonical names regardless of input case", resolved)
	}
	if len(unresolved) != 0 {
		t.Errorf("unresolved = %v, want none", unresolved)
	}

	_, unresolved = resolveProducts([]string{"Wavlet_Toolbox"}, available)
	if len(unresolved) != 1 || unresolved[0].suggestion != "Wavelet_Toolbox" {
		t.Errorf("unresolved = %v, want a Wavelet_Toolbox suggestion", unresolved)
	}
}

func TestAvailableProductsDeterministic(t *testing.T) {
	first := availableProducts("linux", "R2026a")
	if len(first) == 0 {
		t.Fatal("availableProducts(linux, R2026a) returned nothing")
	}
	if !slices.Contains(first, "MATLAB") {
		t.Error("availableProducts(linux, R2026a) missing MATLAB")
	}

	// Assembly iterates maps, so order can vary; the contents must not.
	second := availableProducts("linux", "R2026a")
	slices.Sort(first)
	slices.Sort(second)
	if !slices.Equal(first, second) {
		t.Error("availableProducts returned different contents across calls")
	}
}
