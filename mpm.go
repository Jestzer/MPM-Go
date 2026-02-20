package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	readline "github.com/Jestzer/readlineJestzer"
	"github.com/fatih/color"
)

// Used to read the output of MPM.
type customWriter struct {
	writer io.Writer
}

// mpmSession holds all state accumulated during the interactive CLI session.
type mpmSession struct {
	rl        *readline.Instance
	redText   func(a ...any) string
	greenText func(a ...any) string

	platform        string // "windows", "linux", "macOSx64", "macOSARM"
	defaultTMP      string
	mpmURL          string
	mpmDownloadPath string
	mpmFullPath     string

	release       string
	validReleases []string
	products      []string

	installPath string
	licensePath string
	licenseUsed bool
}

// allReleaseOrder defines the chronological order of all supported releases.
var allReleaseOrder = []string{
	"R2017b", "R2018a", "R2018b", "R2019a", "R2019b", "R2020a", "R2020b",
	"R2021a", "R2021b", "R2022a", "R2022b", "R2023a", "R2023b", "R2024a", "R2024b", "R2025a", "R2025b",
}

var releaseIndexMap = func() map[string]int {
	m := make(map[string]int, len(allReleaseOrder))
	for i, r := range allReleaseOrder {
		m[r] = i
	}
	return m
}()

func releaseIndex(r string) int {
	return releaseIndexMap[r]
}

func newSession() (*mpmSession, error) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt: "> ",
		AutoComplete: readline.NewPrefixCompleter(
			readline.PcItemDynamic(listFiles),
		),
	})
	if err != nil {
		return nil, err
	}

	s := &mpmSession{
		rl:        rl,
		redText:   color.New(color.FgRed).SprintFunc(),
		greenText: color.New(color.FgHiGreen).SprintFunc(),
	}

	// Setup for better Ctrl+C messaging.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		fmt.Println(s.redText("\nExiting from user input."))
		os.Exit(0)
	}()

	return s, nil
}

func main() {
	// Print version number, if requested.
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "-version" {
			fmt.Println("Version number: 2.0")
			os.Exit(0)
		}
	}

	s, err := newSession()
	if err != nil {
		panic(err)
	}
	defer s.rl.Close()

	steps := []func() error{
		s.detectPlatform,
		s.selectAndDownloadMPM,
		s.selectRelease,
		s.selectProducts,
		s.selectInstallPath,
		s.selectLicenseFile,
		s.runMPM,
		s.installLicenseFile,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			fmt.Println(s.redText(err.Error()))
			os.Exit(1)
		}
	}

	fmt.Println(s.greenText("Installation finished! Press the Enter/Return key to close this program."))
	ExitHelper(s.rl)
}

// Figure out your OS.
func (s *mpmSession) detectPlatform() error {
	switch runtime.GOOS {
	case "darwin":
		s.defaultTMP = "/tmp"
		switch runtime.GOARCH {
		case "amd64":
			s.platform = "macOSx64"
			s.mpmURL = "https://www.mathworks.com/mpm/maci64/mpm"
		case "arm64":
			s.platform = "macOSARM"

			// Ask macOSARM users which installer they'd like to use.
			for {
				fmt.Println("Would you like to install an Intel or ARM version of your products? Type in \"intel\", \"arm\" or \"idk\" if you're unsure.")
				manualOSspecified, err := readUserInput(s.rl)
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(s.redText("Exiting from user input."))
					} else {
						fmt.Println(s.redText("Error reading line: ", err))
						continue
					}
					return err
				}

				manualOSspecified = strings.ToLower(strings.TrimSpace(manualOSspecified))

				// Haha yes, I will make you use Intel if you literally type in "idk".
				switch manualOSspecified {
				case "intel", "\"intel\"", "idk", "\"idk\"":
					s.mpmURL = "https://www.mathworks.com/mpm/maci64/mpm"
					s.platform = "macOSx64"
				case "arm", "\"arm\"":
					s.mpmURL = "https://www.mathworks.com/mpm/maca64/mpm"
					s.platform = "macOSARM"
				default:
					fmt.Println(s.redText("Invalid selection. Enter either intel, arm, or idk."))
					continue
				}
				break
			}
		}
	case "windows":
		s.platform = "windows"
		s.defaultTMP = os.Getenv("TMP")
		s.mpmURL = "https://www.mathworks.com/mpm/win64/mpm"

		admin, err := hasAdminRights()
		if err != nil {
			fmt.Println(s.redText("Error checking for administrator rights. This program must be run as an administrator.", err))
			os.Exit(1)
		}
		if !admin {
			fmt.Println(s.redText("Error: This program must be run as an administrator."))
			os.Exit(1)
		}

	case "linux":
		s.platform = "linux"
		s.defaultTMP = "/tmp"
		s.mpmURL = "https://www.mathworks.com/mpm/glnxa64/mpm"
	default:
		fmt.Println(s.redText("Your operating system is unrecognized. Press Enter/Return on your keyboard to close this program."))
		ExitHelper(s.rl)
	}
	return nil
}

// Figure out where you want actual MPM to go and download it.
func (s *mpmSession) selectAndDownloadMPM() error {
	mpmDownloadNeeded := true
	mpmTypeIsMismatched := false

	for {
		fmt.Print("Enter the path to where you would like MPM to download to. " +
			"Press Enter to use \"" + s.defaultTMP + "\"\n> ")
		mpmDownloadPath, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return err
		}
		mpmDownloadPath = strings.TrimSpace(mpmDownloadPath)

		if mpmDownloadPath == "" {
			mpmDownloadPath = s.defaultTMP
		} else {
			_, err := os.Stat(mpmDownloadPath)
			if os.IsNotExist(err) {
				fmt.Printf("The directory \"%s\" does not exist. Do you want to create it? (y/n)\n> ", mpmDownloadPath)
				createDir, err := readUserInput(s.rl)
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(s.redText("Exiting from user input."))
					} else {
						fmt.Println(s.redText("Error reading line: ", err))
						continue
					}
					return err
				}

				createDir = strings.ToLower(strings.TrimSpace(createDir))

				if createDir == "y" || createDir == "yes" || createDir == "t" || createDir == "true" {
					err := os.MkdirAll(mpmDownloadPath, 0755)
					if err != nil {
						fmt.Println(s.redText("Failed to create the directory: ", err, "Please select a different directory."))
						continue
					}
					fmt.Println("Directory created successfully.")
				} else {
					fmt.Println(s.redText("Directory creation skipped. Please select a different directory."))
					continue
				}
			} else if err != nil {
				fmt.Println(s.redText("Error checking the directory: ", err, "Please select a different directory."))
				continue
			}
		}

		s.mpmDownloadPath = mpmDownloadPath

		// Check if MPM already exists in the selected directory.
		fileName := filepath.Join(mpmDownloadPath, "mpm")
		if s.platform == "windows" {
			fileName = filepath.Join(mpmDownloadPath, "mpm.exe")
		}
		_, err = os.Stat(fileName)
		for {
			if err == nil {
				if s.platform == "macOSARM" || s.platform == "macOSx64" {
					fmt.Print("An existing copy of MPM has been detected. Checking which version you downloaded, please wait.\n\n")
					cmd := exec.Command("lipo", "-info", fileName)
					output, err := cmd.Output()
					if err != nil {
						fmt.Println(s.redText("Error checking MPM's file architecture: ", err, ". Please move or delete your existing copy of MPM from the selected directory before proceeding. "+
							"You likely either have a corrupted copy of MPM or it is for Windows or Linux. Press Enter/Return on your keyboard to close this program."))
						ExitHelper(s.rl)
					}
					archInfo := string(output)

					// Warn users if their copy of MPM doesn't match their selected CPU type.
					if strings.Contains(archInfo, "arm64") {
						if s.platform == "macOSx64" {
							mpmTypeIsMismatched = true
						}
					} else if strings.Contains(archInfo, "x86_64") {
						if s.platform == "macOSARM" {
							mpmTypeIsMismatched = true
						}
					} else {
						fmt.Println(s.redText("Error checking MPM's file architecture. Please move or delete your existing copy of MPM from the selected directory before proceeding. Press Enter/Return on your keyboard to close this program."))
						ExitHelper(s.rl)
					}
				}
				if mpmTypeIsMismatched {
					fmt.Println("MPM already exists in this directory and is for a different CPU architecture than you selected. Would you like to overwrite it?")
				} else {
					fmt.Println("MPM already exists in this directory. Would you like to overwrite it?")
				}
				overwriteMPM, err := readUserInput(s.rl)
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(s.redText("Exiting from user input."))
					} else {
						fmt.Println(s.redText("Error reading line: ", err))
						continue
					}
					return err
				}

				overwriteMPM = strings.TrimSpace(strings.ToLower(overwriteMPM))

				if overwriteMPM == "n" || overwriteMPM == "no" || overwriteMPM == "f" || overwriteMPM == "false" {
					if mpmTypeIsMismatched { // Make up your mind. Do you want to use ARM or Intel?
						fmt.Println(s.redText("You can't use a version of MPM that doesn't match the CPU architecture you selected. Please either select a different directory to download " +
							"MPM or move your existing copy elsewhere. Press Enter/Return on your keyboard to close this program."))
						ExitHelper(s.rl)
					} else {
						fmt.Println("Skipping download.")
						mpmDownloadNeeded = false
						break
					}
				}

				if overwriteMPM == "y" || overwriteMPM == "yes" || overwriteMPM == "t" || overwriteMPM == "true" {
					break
				} else {
					fmt.Println(s.redText("Invalid choice. Please enter either 'y' or 'n'."))
					continue
				}
			}
			break
		}

		// Download MPM.
		if mpmDownloadNeeded {
			fmt.Println("Downloading MPM. Please wait.")
			err = downloadFile(s.mpmURL, fileName)
			if err != nil {
				fmt.Println(s.redText("Failed to download MPM. ", err))
				os.Exit(1)
			}
			fmt.Println("MPM downloaded successfully.")
		}

		// Make sure you can actually execute MPM on Linux and macOS.
		if s.platform != "windows" {
			cmd := exec.Command("chmod", "+x", filepath.Join(mpmDownloadPath, "mpm"))
			err := cmd.Run()

			if err != nil {
				fmt.Println("Failed to execute the command: ", err)
				fmt.Print(". Either select a different directory, run this program with needed privileges, " +
					"or make modifications to MPM outside of this program.")
				continue
			}
		}
		break
	}
	return nil
}

// Ask the user which release they'd like to install.
func (s *mpmSession) selectRelease() error {
	if s.platform == "macOSARM" {
		s.validReleases = []string{
			"R2023b", "R2024a", "R2024b", "R2025a", "R2025b",
		}
	} else {
		s.validReleases = []string{
			"R2017b", "R2018a", "R2018b", "R2019a", "R2019b", "R2020a", "R2020b",
			"R2021a", "R2021b", "R2022a", "R2022b", "R2023a", "R2023b", "R2024a", "R2024b", "R2025a", "R2025b",
		}
	}

	defaultRelease := "R2025b"

	for {
		fmt.Printf("Enter which release you would like to install. Press Enter to select %s: ", defaultRelease)
		fmt.Print("\n> ")
		release, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return err
		}

		release = strings.TrimSpace(release)
		if release == "" {
			release = defaultRelease
		}

		release = strings.ToLower(release)
		found := false
		for _, validRelease := range s.validReleases {
			if strings.ToLower(validRelease) == release {
				release = validRelease
				found = true
				break
			}
		}

		if found {
			s.release = release
			break
		}

		if s.platform == "macOSARM" {
			fmt.Println(s.redText("Invalid release. Enter a release between R2023b-R2025b."))
		} else {
			fmt.Println(s.redText("Invalid release. Enter a release between R2017b-R2025b."))
		}
	}
	return nil
}

// Product selection and validation.
func (s *mpmSession) selectProducts() error {
	for {
		fmt.Print("Enter the products you would like to install. Use the same syntax as MPM to specify products. " +
			"Press Enter to install all products.\n> ")
		productsInput, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return err
		}

		productsInput = strings.TrimSpace(productsInput)

		// Begin assembling the full product list based on your release and platform.
		// This is to ensure the products you're specifying exist or that a full list is assembled if you decide to install everything.
		// Notes:
		// - No oldProductsToAdd is needed for macOSARM at the moment (apart from R2024b).
		// - No new products were added in R2024a, R2024b, R2025a, nor R2025b for any platform, so they are omitted entries.
		var newProductsToAdd map[string]string
		var oldProductsToAdd map[string]string
		var allProducts []string

		// Let's start with defining the "new" products to add.
		switch s.platform {
		case "windows":
			newProductsToAdd = map[string]string{
				"R2023b": "Simulink_Fault_Analyzer Polyspace_Test",
				"R2023a": "MATLAB_Test C2000_Microcontroller_Blockset",
				"R2022b": "Medical_Imaging_Toolbox Simscape_Battery",
				"R2022a": "Wireless_Testbench Bluetooth_Toolbox DSP_HDL_Toolbox Requirements_Toolbox Industrial_Communication_Toolbox",
				"R2021b": "Signal_Integrity_Toolbox RF_PCB_Toolbox",
				"R2021a": "Satellite_Communications_Toolbox DDS_Blockset",
				"R2020b": "UAV_Toolbox Radar_Toolbox Lidar_Toolbox Deep_Learning_HDL_Toolbox",
				"R2020a": "Simulink_Compiler Motor_Control_Blockset MATLAB_Web_App_Server Wireless_HDL_Toolbox",
				"R2019b": "ROS_Toolbox Navigation_Toolbox",
				"R2019a": "System_Composer SoC_Blockset SerDes_Toolbox Reinforcement_Learning_Toolbox Audio_Toolbox Mixed-Signal_Blockset AUTOSAR_Blockset MATLAB_Parallel_Server Polyspace_Bug_Finder_Server Polyspace_Code_Prover_Server Automated_Driving_Toolbox Computer_Vision_Toolbox",
				"R2018b": "Communications_Toolbox Simscape_Electrical Sensor_Fusion_and_Tracking_Toolbox Deep_Learning_Toolbox 5G_Toolbox WLAN_Toolbox LTE_Toolbox",
				"R2018a": "Predictive_Maintenance_Toolbox Vehicle_Dynamics_Blockset",
				"R2017b": "Aerospace_Blockset Aerospace_Toolbox Antenna_Toolbox Bioinformatics_Toolbox Control_System_Toolbox Curve_Fitting_Toolbox DSP_System_Toolbox Data_Acquisition_Toolbox Database_Toolbox Datafeed_Toolbox Econometrics_Toolbox Embedded_Coder Financial_Instruments_Toolbox Financial_Toolbox Fixed-Point_Designer Fuzzy_Logic_Toolbox GPU_Coder Global_Optimization_Toolbox HDL_Coder HDL_Verifier Image_Acquisition_Toolbox Image_Processing_Toolbox Instrument_Control_Toolbox MATLAB MATLAB_Coder MATLAB_Compiler MATLAB_Compiler_SDK MATLAB_Production_Server MATLAB_Report_Generator Mapping_Toolbox Model_Predictive_Control_Toolbox Model-Based_Calibration_Toolbox Network_License_Manager Optimization_Toolbox Parallel_Computing_Toolbox Partial_Differential_Equation_Toolbox Phased_Array_System_Toolbox Polyspace_Bug_Finder Polyspace_Code_Prover Powertrain_Blockset RF_Blockset RF_Toolbox Risk_Management_Toolbox Robotics_System_Toolbox Robust_Control_Toolbox Signal_Processing_Toolbox SimBiology SimEvents Simscape Simscape_Driveline Simscape_Fluids Simscape_Multibody Simulink Simulink_3D_Animation Simulink_Check Simulink_Coder Simulink_Control_Design Simulink_Coverage Simulink_Design_Optimization Simulink_Design_Verifier Simulink_Desktop_Real-Time Simulink_PLC_Coder Simulink_Real-Time Simulink_Report_Generator Simulink_Test Spreadsheet_Link Stateflow Statistics_and_Machine_Learning_Toolbox Symbolic_Math_Toolbox System_Identification_Toolbox Text_Analytics_Toolbox Vehicle_Network_Toolbox Vision_HDL_Toolbox Wavelet_Toolbox",
			}

		case "linux":
			newProductsToAdd = map[string]string{
				"R2023b": "Simulink_Fault_Analyzer Polyspace_Test Simulink_Desktop_Real-Time",
				"R2023a": "MATLAB_Test C2000_Microcontroller_Blockset",
				"R2022b": "Medical_Imaging_Toolbox Simscape_Battery",
				"R2022a": "Wireless_Testbench Simulink_Real-Time Bluetooth_Toolbox DSP_HDL_Toolbox Requirements_Toolbox Industrial_Communication_Toolbox",
				"R2021b": "Signal_Integrity_Toolbox RF_PCB_Toolbox",
				"R2021a": "Satellite_Communications_Toolbox DDS_Blockset",
				"R2020b": "UAV_Toolbox Radar_Toolbox Lidar_Toolbox Deep_Learning_HDL_Toolbox",
				"R2020a": "Simulink_Compiler Motor_Control_Blockset MATLAB_Web_App_Server Wireless_HDL_Toolbox",
				"R2019b": "ROS_Toolbox Simulink_PLC_Coder Navigation_Toolbox",
				"R2019a": "System_Composer SoC_Blockset SerDes_Toolbox Reinforcement_Learning_Toolbox Audio_Toolbox Mixed-Signal_Blockset AUTOSAR_Blockset MATLAB_Parallel_Server Polyspace_Bug_Finder_Server Polyspace_Code_Prover_Server Automated_Driving_Toolbox Computer_Vision_Toolbox",
				"R2018b": "Communications_Toolbox Simscape_Electrical Sensor_Fusion_and_Tracking_Toolbox Deep_Learning_Toolbox 5G_Toolbox WLAN_Toolbox LTE_Toolbox",
				"R2018a": "Predictive_Maintenance_Toolbox Vehicle_Network_Toolbox Vehicle_Dynamics_Blockset",
				"R2017b": "Aerospace_Blockset Aerospace_Toolbox Antenna_Toolbox Bioinformatics_Toolbox Control_System_Toolbox Curve_Fitting_Toolbox DSP_System_Toolbox Database_Toolbox Datafeed_Toolbox Econometrics_Toolbox Embedded_Coder Financial_Instruments_Toolbox Financial_Toolbox Fixed-Point_Designer Fuzzy_Logic_Toolbox GPU_Coder Global_Optimization_Toolbox HDL_Coder HDL_Verifier Image_Acquisition_Toolbox Image_Processing_Toolbox Instrument_Control_Toolbox MATLAB MATLAB_Coder MATLAB_Compiler MATLAB_Compiler_SDK MATLAB_Production_Server MATLAB_Report_Generator Mapping_Toolbox Model_Predictive_Control_Toolbox Network_License_Manager Optimization_Toolbox Parallel_Computing_Toolbox Partial_Differential_Equation_Toolbox Phased_Array_System_Toolbox Polyspace_Bug_Finder Polyspace_Code_Prover Powertrain_Blockset RF_Blockset RF_Toolbox Risk_Management_Toolbox Robotics_System_Toolbox Robust_Control_Toolbox Signal_Processing_Toolbox SimBiology SimEvents Simscape Simscape_Driveline Simscape_Fluids Simscape_Multibody Simulink Simulink_3D_Animation Simulink_Check Simulink_Coder Simulink_Control_Design Simulink_Coverage Simulink_Design_Optimization Simulink_Design_Verifier Simulink_Report_Generator Simulink_Test Stateflow Statistics_and_Machine_Learning_Toolbox Symbolic_Math_Toolbox System_Identification_Toolbox Text_Analytics_Toolbox Vision_HDL_Toolbox Wavelet_Toolbox",
			}

		case "macOSx64":
			newProductsToAdd = map[string]string{
				"R2023b": "Simulink_Fault_Analyzer Polyspace_Test",
				"R2023a": "MATLAB_Test",
				"R2022b": "Medical_Imaging_Toolbox Simscape_Battery",
				"R2022a": "Bluetooth_Toolbox DSP_HDL_Toolbox Requirements_Toolbox Industrial_Communication_Toolbox",
				"R2021b": "RF_PCB_Toolbox",
				"R2021a": "Satellite_Communications_Toolbox DDS_Blockset",
				"R2020b": "UAV_Toolbox Radar_Toolbox Lidar_Toolbox",
				"R2020a": "Simulink_Compiler Motor_Control_Blockset MATLAB_Web_App_Server Wireless_HDL_Toolbox",
				"R2019b": "ROS_Toolbox Simulink_PLC_Coder Navigation_Toolbox",
				"R2019a": "System_Composer SerDes_Toolbox Reinforcement_Learning_Toolbox Audio_Toolbox Mixed-Signal_Blockset AUTOSAR_Blockset Polyspace_Bug_Finder_Server Polyspace_Code_Prover_Server Automated_Driving_Toolbox Computer_Vision_Toolbox",
				"R2018b": "Communications_Toolbox Simscape_Electrical Sensor_Fusion_and_Tracking_Toolbox Deep_Learning_Toolbox 5G_Toolbox WLAN_Toolbox LTE_Toolbox",
				"R2018a": "Predictive_Maintenance_Toolbox Vehicle_Dynamics_Blockset",
				"R2017b": "Aerospace_Blockset Aerospace_Toolbox Antenna_Toolbox Bioinformatics_Toolbox Control_System_Toolbox Curve_Fitting_Toolbox DSP_System_Toolbox Database_Toolbox Datafeed_Toolbox Econometrics_Toolbox Embedded_Coder Financial_Instruments_Toolbox Financial_Toolbox Fixed-Point_Designer Fuzzy_Logic_Toolbox Global_Optimization_Toolbox HDL_Coder Image_Acquisition_Toolbox Image_Processing_Toolbox Instrument_Control_Toolbox MATLAB MATLAB_Coder MATLAB_Compiler MATLAB_Compiler_SDK MATLAB_Production_Server MATLAB_Report_Generator Mapping_Toolbox Model_Predictive_Control_Toolbox Network_License_Manager Optimization_Toolbox Parallel_Computing_Toolbox Partial_Differential_Equation_Toolbox Phased_Array_System_Toolbox Polyspace_Bug_Finder Polyspace_Code_Prover Powertrain_Blockset RF_Blockset RF_Toolbox Risk_Management_Toolbox Robotics_System_Toolbox Robust_Control_Toolbox Signal_Processing_Toolbox SimBiology SimEvents Simscape Simscape_Driveline Simscape_Fluids Simscape_Multibody Simulink Simulink_3D_Animation Simulink_Check Simulink_Coder Simulink_Control_Design Simulink_Coverage Simulink_Design_Optimization Simulink_Design_Verifier Simulink_Desktop_Real-Time Simulink_Report_Generator Simulink_Test Stateflow Statistics_and_Machine_Learning_Toolbox Symbolic_Math_Toolbox System_Identification_Toolbox Text_Analytics_Toolbox Wavelet_Toolbox",
			}

		case "macOSARM":
			newProductsToAdd = map[string]string{
				"R2023b": "5G_Toolbox AUTOSAR_Blockset Aerospace_Blockset Aerospace_Toolbox Antenna_Toolbox Audio_Toolbox Automated_Driving_Toolbox Bioinformatics_Toolbox Bluetooth_Toolbox Communications_Toolbox Computer_Vision_Toolbox Control_System_Toolbox Curve_Fitting_Toolbox DDS_Blockset DSP_HDL_Toolbox DSP_System_Toolbox Database_Toolbox Datafeed_Toolbox Deep_Learning_Toolbox Econometrics_Toolbox Embedded_Coder Financial_Instruments_Toolbox Financial_Toolbox Fixed-Point_Designer Fuzzy_Logic_Toolbox Global_Optimization_Toolbox HDL_Coder Image_Acquisition_Toolbox Image_Processing_Toolbox Industrial_Communication_Toolbox Instrument_Control_Toolbox LTE_Toolbox Lidar_Toolbox MATLAB MATLAB_Coder MATLAB_Compiler MATLAB_Compiler_SDK MATLAB_Report_Generator MATLAB_Test Mapping_Toolbox Medical_Imaging_Toolbox Mixed-Signal_Blockset Model_Predictive_Control_Toolbox Motor_Control_Blockset Navigation_Toolbox Network_License_Manager Optimization_Toolbox Parallel_Computing_Toolbox Partial_Differential_Equation_Toolbox Phased_Array_System_Toolbox Powertrain_Blockset Predictive_Maintenance_Toolbox RF_Blockset RF_PCB_Toolbox RF_Toolbox ROS_Toolbox Radar_Toolbox Reinforcement_Learning_Toolbox Requirements_Toolbox Risk_Management_Toolbox Robotics_System_Toolbox Robust_Control_Toolbox Satellite_Communications_Toolbox Sensor_Fusion_and_Tracking_Toolbox SerDes_Toolbox Signal_Processing_Toolbox SimBiology SimEvents Simscape Simscape_Battery Simscape_Driveline Simscape_Electrical Simscape_Fluids Simscape_Multibody Simulink Simulink_3D_Animation Simulink_Check Simulink_Coder Simulink_Compiler Simulink_Control_Design Simulink_Coverage Simulink_Design_Optimization Simulink_Design_Verifier Simulink_Fault_Analyzer Simulink_PLC_Coder Simulink_Report_Generator Simulink_Test Stateflow Statistics_and_Machine_Learning_Toolbox Symbolic_Math_Toolbox System_Composer System_Identification_Toolbox Text_Analytics_Toolbox UAV_Toolbox Vehicle_Dynamics_Blockset WLAN_Toolbox Wavelet_Toolbox Wireless_HDL_Toolbox",
			}
		}

		// Use a loop to go through the list above to add the appropriate products.
		selectedIdx := releaseIndex(s.release)
		for releaseLoop, product := range newProductsToAdd {
			if selectedIdx >= releaseIndex(releaseLoop) {
				allProducts = append(allProducts, strings.Fields(product)...)
			}
		}

		// Old products to add.
		switch s.platform {
		case "windows":
			oldProductsToAdd = map[string]string{
				"R2024b": "Filter_Design_HDL_Coder",
				"R2021b": "Simulink_Requirements OPC_Toolbox",
				"R2020b": "Trading_Toolbox",
				"R2019b": "LTE_HDL_Toolbox",
				"R2018b": "Audio_System_Toolbox Automated_Driving_System_Toolbox Computer_Vision_System_Toolbox MATLAB_Distributed_Computing_Server",
				"R2018a": "Communications_System_Toolbox LTE_System_Toolbox Neural_Network_Toolbox Simscape_Electronics Simscape_Power_Systems WLAN_System_Toolbox",
			}

		case "linux":
			oldProductsToAdd = map[string]string{
				"R2024b": "Filter_Design_HDL_Coder",
				"R2021b": "Simulink_Requirements",
				"R2020b": "Trading_Toolbox",
				"R2019b": "LTE_HDL_Toolbox",
				"R2018b": "Audio_System_Toolbox Automated_Driving_System_Toolbox Computer_Vision_System_Toolbox MATLAB_Distributed_Computing_Server",
				"R2018a": "Communications_System_Toolbox LTE_System_Toolbox Neural_Network_Toolbox Simscape_Electronics Simscape_Power_Systems WLAN_System_Toolbox",
			}

		case "macOSx64":
			oldProductsToAdd = map[string]string{
				"R2024b": "Filter_Design_HDL_Coder",
				"R2021b": "Simulink_Requirements MATLAB_Parallel_Server",
				"R2020b": "Trading_Toolbox",
				"R2019b": "LTE_HDL_Toolbox",
				"R2018b": "Audio_System_Toolbox Automated_Driving_System_Toolbox Computer_Vision_System_Toolbox MATLAB_Distributed_Computing_Server",
				"R2018a": "Communications_System_Toolbox LTE_System_Toolbox Neural_Network_Toolbox Simscape_Electronics Simscape_Power_Systems WLAN_System_Toolbox",
			}
		case "macOSARM":
			oldProductsToAdd = map[string]string{
				"R2024b": "Filter_Design_HDL_Coder",
			}
		}

		// The actual for loop that goes through the list above. Note that it uses the same logic as newProducts, it just uses <= instead of >=.
		for releaseLoop, product := range oldProductsToAdd {
			if selectedIdx <= releaseIndex(releaseLoop) {
				allProducts = append(allProducts, strings.Fields(product)...)
			}
		}

		// Determine the products we'll actually be using with MPM.
		if productsInput == "" {
			s.products = allProducts
		} else if productsInput == "parallel_products" {
			if selectedIdx <= releaseIndex("R2018b") {
				s.products = []string{"MATLAB", "Parallel_Computing_Toolbox", "MATLAB_Distributed_Computing_Server"}
			} else {
				s.products = []string{"MATLAB", "Parallel_Computing_Toolbox", "MATLAB_Parallel_Server"}
			}
		} else {
			s.products = strings.Fields(productsInput)
			missingProducts := checkProductsExist(s.products, allProducts)
			if len(missingProducts) > 0 {
				fmt.Println(s.redText("The following products do not exist:"))
				for _, missingProduct := range missingProducts {
					fmt.Println(s.redText("- " + missingProduct))
				}
				fmt.Println(s.redText("Please try again and check for any typos. Different products should be separated by spaces. Spaces in a product name should be replaced with underscores."))
				continue
			}
		}
		break
	}
	return nil
}

// Select the installation path.
func (s *mpmSession) selectInstallPath() error {
	// Set the default installation path based on your OS.
	var defaultInstallationPath string
	switch {
	case s.platform == "macOSx64" || s.platform == "macOSARM":
		defaultInstallationPath = "/Applications/MATLAB_" + s.release + ".app"
	case s.platform == "windows":
		defaultInstallationPath = "C:\\Program Files\\MATLAB\\" + s.release
	case s.platform == "linux":
		defaultInstallationPath = "/usr/local/MATLAB/" + s.release
	}

	for {
		fmt.Print("Enter the full path where you would like to install these products. "+
			"Press Enter to install to default path: \"", defaultInstallationPath, "\"\n> ")

		installPath, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return err
		}

		installPath = strings.TrimSpace(installPath)

		if installPath == "" {
			installPath = defaultInstallationPath
		} else {
			if _, err := os.Stat(installPath); os.IsNotExist(err) {
				if err := os.MkdirAll(installPath, 0755); err != nil {
					fmt.Println(s.redText("Error creating directory: ", err, " Please pick a different installation path."))
					continue
				} else {
					fullPath, err := filepath.Abs(installPath)
					if err != nil {
						fmt.Println(s.redText("Error reading newly-created directory's full path: ", err, " Please pick a different installation path."))
						continue
					} else {
						fmt.Println("Directory successfully created:", fullPath)
					}
				}
			} else if err != nil {
				fullPath, _ := filepath.Abs(installPath)
				fmt.Println(s.redText("Error selecting directory: ", fullPath, " Please pick a different installation path."))
				continue
			}
		}

		s.installPath = installPath
		break
	}
	return nil
}

// Optional license file selection.
func (s *mpmSession) selectLicenseFile() error {
	for {
		fmt.Print("If you have a license file you'd like to include in your installation, " +
			"please provide the full path to the existing license file.\n> ")

		licensePath, err := readUserInput(s.rl)
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(s.redText("Exiting from user input."))
			} else {
				fmt.Println(s.redText("Error reading line: ", err))
				continue
			}
			return err
		}
		licensePath = strings.TrimSpace(licensePath)

		if licensePath == "" {
			s.licenseUsed = false
			break
		} else {
			// Check if the license file exists and has the correct extension.
			_, err := os.Stat(licensePath)
			if err != nil {
				fmt.Println(s.redText("Error: ", err))
				continue
			} else if !strings.HasSuffix(licensePath, ".dat") && !strings.HasSuffix(licensePath, ".lic") && !strings.HasSuffix(licensePath, ".xml") {
				fmt.Println(s.redText("Invalid file extension. Please provide a file with a .dat, .lic, or .xml file extension."))
				continue
			} else {
				s.licenseUsed = true
				s.licensePath = licensePath
				break
			}
		}
	}
	return nil
}

// Construct the command and run MPM.
func (s *mpmSession) runMPM() error {
	fmt.Println("Loading, please wait.")

	mpmBinary := "mpm"
	if s.platform == "windows" {
		mpmBinary = "mpm.exe"
	}
	s.mpmFullPath = filepath.Join(s.mpmDownloadPath, mpmBinary)

	cmdArgs := []string{
		s.mpmFullPath,
		"install",
		"--release=" + s.release,
		"--destination=" + s.installPath,
		"--products",
	}
	cmdArgs = append(cmdArgs, s.products...)

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)

	// Use customWriter to intercept and process MPM's output.
	cmd.Stdout = &customWriter{writer: os.Stdout}
	cmd.Stderr = &customWriter{writer: os.Stderr}
	err := cmd.Run() // Run it already geeeeeeeez.

	if err != nil {
		errString := err.Error()
		if strings.Contains(errString, "mpm: no such file or directory") || strings.Contains(errString, "mpm.exe: no such file or directory") {
			fmt.Println(s.redText("MPM was either moved, renamed, deleted, or you've lost permissions to access it. Press the Enter/Return key to close this program."))
		} else {
			fmt.Println(s.redText("An error occurred during installation. See the error above for more information. ", err, ". Press the Enter/Return key to close this program."))
		}
		ExitHelper(s.rl)
	}
	return nil
}

// Create the licenses directory and copy the license file, if one was specified.
func (s *mpmSession) installLicenseFile() error {
	if !s.licenseUsed {
		return nil
	}

	// Create the licenses directory.
	licensesDir := filepath.Join(s.installPath, "licenses")
	if err := os.Mkdir(licensesDir, 0755); err != nil && !os.IsExist(err) {
		fmt.Println(s.redText("Error creating \"licenses\" directory: ", err, ". You will need to manually place your license file in your installation."))
		return nil
	}

	// Copy the license file to the "licenses" directory.
	destPath := filepath.Join(licensesDir, filepath.Base(s.licensePath))

	src, err := os.Open(s.licensePath)
	if err != nil {
		fmt.Println(s.redText("Error opening license file: ", err, ". You will need to manually place your license file in your installation."))
		return nil
	}
	defer src.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		fmt.Println(s.redText("Error creating destination file: ", err, ". You will need to manually place your license file in your installation."))
		return nil
	}
	defer dest.Close()

	if _, err = io.Copy(dest, src); err != nil {
		fmt.Println(s.redText("Error copying license file: ", err, ". You will need to manually place your license file in your installation."))
	}
	return nil
}

// hasAdminRights checks for admin privileges by attempting to create a temp file
// in the Windows root directory. This is a pragmatic check rather than a proper
// Windows API call (which would require golang.org/x/sys/windows).
// Limitation: may produce false negatives if root-dir creation is restricted
// for reasons other than admin rights (e.g. antivirus or disk policies).
func hasAdminRights() (bool, error) {

	// Find out where Windows is installed.
	winDir := os.Getenv("WINDIR")
	if winDir == "" {
		return false, fmt.Errorf("windir environment variable not found")
	}

	// Extract the root drive (e.g., "C:\").
	rootDir := filepath.VolumeName(winDir) + `\`

	testFile := filepath.Join(rootDir, "admin_test")
	file, err := os.Create(testFile)
	if err != nil {
		return false, nil // You don't have admin rights!
	}
	file.Close()

	err = os.Remove(testFile)
	if err != nil {
		return false, fmt.Errorf("failed to delete file made when testing admin rights: %w", err) // How awkward would that be??
	}

	return true, nil
}

func downloadFile(url string, filePath string) error {
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d %s", response.StatusCode, response.Status)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, response.Body)
	return err
}

// Make sure the products you've specified exist.
func checkProductsExist(inputProducts []string, availableProducts []string) []string {
	productSet := make(map[string]struct{}, len(availableProducts))
	for _, product := range availableProducts {
		productSet[product] = struct{}{}
	}

	var missingProducts []string
	for _, inputProduct := range inputProducts {
		if _, exists := productSet[inputProduct]; !exists {
			missingProducts = append(missingProducts, inputProduct)
		}
	}
	return missingProducts
}

// Reading user input in a separate function allows me to accept input such as "quit" or "exit" without needing to repeat said code.
func readUserInput(rl *readline.Instance) (string, error) {
	redText := color.New(color.FgRed).SprintFunc()
	line, err := rl.Readline()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	line = os.ExpandEnv(line)

	// We want to separate the lowercase version for just exiting and quitting, since it'll otherwise affect product name input.
	lineLower := strings.ToLower(line)

	if lineLower == "exit" || lineLower == "quit" {
		fmt.Println(redText("\nExiting from user input."))
		os.Exit(0)
	}
	return line, nil
}

// List and auto-complete files and folders with tabbing.
func listFiles(line string) []string {
	dir, file := filepath.Split(line)
	if dir == "" {
		dir = "."
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var suggestions []string
	for _, f := range files {
		name := f.Name()
		if f.IsDir() {
			name += string(os.PathSeparator)
		}
		if strings.HasPrefix(name, file) {
			suggestions = append(suggestions, filepath.Join(dir, name))
		}
	}

	return suggestions
}

// Function used to write a more meaningful installation message. Needs to be in here and not the main function.
func (cw *customWriter) Write(p []byte) (n int, err error) {
	output := string(p)
	n, err = cw.writer.Write(p) // Write MPM's original message first.
	if err != nil {
		return n, err
	}
	if strings.Contains(output, "Starting install") {
		fmt.Fprintln(cw.writer, "Installation has begun. Please wait while it finishes. There is no progress indicator.")
	}
	return n, nil
}

// For the double-clickers.
func ExitHelper(rl *readline.Instance) {
	if rl == nil {
		fmt.Scanln()
		os.Exit(0)
	}
	rl.SetPrompt("")
	_, err := rl.Readline()
	if err != nil {
		redText := color.New(color.FgRed).SprintFunc()
		fmt.Println(redText("Exiting from user input."))
	}
	os.Exit(0)
}
