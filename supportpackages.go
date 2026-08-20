package main

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io"
	"strings"
)

// Reference input files published by MathWorks (see mpm-input-files/LICENSE.md),
// listing every product and support package per release. Support-package names
// are read from these so the lists don't need to be maintained by hand;
// supporting a new release just means vendoring its file into mpm-input-files.
//
//go:embed mpm-input-files
var mpmInputFS embed.FS

// vendorLicenseKeywords match the handful of support packages that MPM will
// only install with --accept-vendor-licenses. Keywords are used instead of
// exact names because names vary slightly between releases.
var vendorLicenseKeywords = []string{
	"genicam", "gige_vision", "ip_cameras", "parrot", "ryze_tello", "micro:bit",
}

func requiresVendorLicense(pkg string) bool {
	lower := strings.ToLower(pkg)
	for _, keyword := range vendorLicenseKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// supportPackagesForRelease returns the support packages available for a release.
// Releases predating support packages (R2018b and older) and releases without a
// vendored input file return nothing.
func supportPackagesForRelease(release string) []string {
	data, err := mpmInputFS.ReadFile("mpm-input-files/" + release + "/mpm_input_" + strings.ToLower(release) + ".txt")
	if err != nil {
		return nil
	}
	return parseSupportPackages(bytes.NewReader(data))
}

// parseSupportPackages extracts support-package names from a MathWorks mpm
// input file: "#product.X" lines between the "## SUPPORT PACKAGES" section
// title and the next section title (OPTIONAL FEATURES or CHECKSUM, depending
// on the release).
func parseSupportPackages(r io.Reader) []string {
	var pkgs []string
	inSection := false
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if isSectionTitle(line) {
			inSection = line == "## SUPPORT PACKAGES"
			continue
		}
		if !inSection {
			continue
		}
		name, found := strings.CutPrefix(line, "#product.")
		if !found {
			name, found = strings.CutPrefix(line, "product.")
		}
		if !found {
			continue
		}
		// R2019b's file wraps one name in stray underscores.
		name = strings.Trim(name, "_")
		if name != "" {
			pkgs = append(pkgs, name)
		}
	}
	return pkgs
}

// isSectionTitle reports whether a line is an all-caps section title like
// "## SUPPORT PACKAGES", as opposed to an explanatory "## ..." comment, which
// the input files also place inside sections.
func isSectionTitle(line string) bool {
	title, found := strings.CutPrefix(line, "## ")
	if !found || title == "" {
		return false
	}
	for _, r := range title {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != ' ' {
			return false
		}
	}
	return true
}

// Optional support-package selection. MPM installs support packages through the
// same --products list as regular products, and MPM itself pulls in whichever
// base products a support package requires.
func (s *mpmSession) selectSupportPackages() error {
	pkgs := supportPackagesForRelease(s.release)
	if len(pkgs) == 0 {
		// Releases before R2019a have no support packages to offer.
		return nil
	}

	fmt.Println("Note: not every support package is available for every operating system. " +
		"MPM will report an error if you select one that isn't available for yours.")

	for {
		fmt.Print("Enter any support packages you would like to install. Use the same syntax as MPM to specify support packages. " +
			"Type \"list\" to see all support packages available for your release. Press Enter to skip installing support packages.\n> ")
		packagesInput, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return err
		}

		packagesInput = strings.TrimSpace(packagesInput)

		if packagesInput == "" {
			return nil
		}

		if strings.EqualFold(packagesInput, "list") {
			printColumns(pkgs)
			continue
		}

		selected, unresolved := resolveProducts(strings.Fields(packagesInput), pkgs)
		if len(unresolved) > 0 {
			fmt.Println(s.redText("The following support packages were not recognized:"))
			for _, u := range unresolved {
				if u.suggestion != "" {
					fmt.Printf("  %s  did you mean %s?\n", s.redText("- "+u.input), s.greenText(u.suggestion))
				} else {
					fmt.Println(s.redText("- " + u.input))
				}
			}
			// Unlike products, unrecognized names may be allowed through, since MathWorks
			// adds support packages mid-release and this program's lists can lag behind.
			fmt.Print("They may exist if they are newer than this program's lists. Would you like to attempt to install them anyway? (y/n)\n> ")
			installAnyway, err := s.askYesNo()
			if err != nil {
				return err
			}
			if !installAnyway {
				fmt.Println(s.redText("Please try again. Different support packages should be separated by spaces. Spaces in a name should be replaced with underscores."))
				continue
			}
			for _, u := range unresolved {
				selected = append(selected, u.input)
			}
		}

		var vendorPkgs []string
		for _, pkg := range selected {
			if requiresVendorLicense(pkg) {
				vendorPkgs = append(vendorPkgs, pkg)
			}
		}
		if len(vendorPkgs) > 0 {
			fmt.Println("The following support packages require you to accept vendor licenses to install them:")
			for _, pkg := range vendorPkgs {
				fmt.Println("- " + pkg)
			}
			fmt.Print("Do you accept the vendor licenses for these support packages? (y/n)\n> ")
			accepted, err := s.askYesNo()
			if err != nil {
				return err
			}
			if !accepted {
				fmt.Println(s.redText("Vendor licenses were not accepted. Please re-enter your support packages without them, or press Enter to skip installing support packages."))
				continue
			}
			s.acceptVendorLicenses = true
		}

		s.supportPackages = selected
		return nil
	}
}

// askYesNo reads input until it gets a yes or no answer.
func (s *mpmSession) askYesNo() (bool, error) {
	for {
		answer, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return false, err
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes", "t", "true":
			return true, nil
		case "n", "no", "f", "false":
			return false, nil
		default:
			fmt.Println(s.redText("Invalid choice. Please enter either 'y' or 'n'."))
		}
	}
}
