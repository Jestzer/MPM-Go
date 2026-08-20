# MPM Wrapper Written in Go
A wrapper that allows you to interactively install MathWorks Products using MPM (MATLAB Package Manager.) This software is not associated with or created by MathWorks. This supports installing MATLAB toolboxes, adjacent products, and support packages (R2019a and later.) You will not be given the option to download or use offline installation files.

Support package names are read from MathWorks' published mpm input files, vendored in the mpm-input-files directory from [mathworks-ref-arch/matlab-dockerfile](https://github.com/mathworks-ref-arch/matlab-dockerfile) under the license in mpm-input-files/LICENSE.md. To add a new release, copy its input file into that directory and rebuild.

Usage: run the program by either double-clicking on it (if your setup supports this) or by running it through the command line. Follow the prompts as given.

If you'd like to print the version number, add the argument "-version" when starting the program.

If you want a compiled released for a platform that is not listed in the Releases, please let me know (ex: Windows 11, macOS, Arch Linux, etc.)

To-do:
- Prompt for admin rights when using Windows
- Separate all MATLAB products from all Polyspace products to avoid issues later on (such as when updating)
