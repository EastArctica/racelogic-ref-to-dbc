# Racelogic .ref to .dbc Extractor

A command-line tool that converts Racelogic `.ref` files into standard `.dbc` (CAN Database) files.

## What It Does

This tool reads a binary `.ref` file, finds all the zlib-compressed CAN signal definitions inside it, and writes them out to a properly formatted `.dbc` file.

## Installation

You can download a pre-built program or build it yourself.

### From GitHub Releases (Recommended)

1.  Go to the **[Releases](https://github.com/EastArctica/racelogic-ref-to-dbc/releases)** page.
2.  Download the executable for your operating system (Windows, macOS, or Linux).
3.  Continue to [Usage](#usage)

### From Source (For Developers)

If you have Go installed, you can build it from source.

```bash
# 1. Make sure you have Go installed ([https://go.dev/dl/](https://go.dev/dl/))
# 2. Clone the repo and navigate into it
git clone [https://github.com/EastArctica/racelogic-ref-to-dbc.git](https://github.com/EastArctica/racelogic-ref-to-dbc.git)
cd racelogic-ref-to-dbc

# 3. Build the executable
go build
````

## Usage

The tool is designed to be used from the command line, which also makes it easy to script.

### Drag and Drop

The simplest way to use it is to drag your `.ref` file onto the `racelogic-ref-to-dbc` executable. It will create the `.dbc` file in the same directory.

### Command Line / Scripting

You can run the tool from your terminal. This is useful for automating conversions.

```bash
# Basic use:
# The input file is the first argument.
./racelogic-ref-to-dbc /path/to/your/file.ref

# For more control, use flags for input and output files:
# -i: specify the input file
# -o: specify the output file
./racelogic-ref-to-dbc -i /path/to/file.ref -o /path/to/custom_output.dbc
```

## Error Messages

If something goes wrong (e.g., the file is corrupt, a line is malformed), the program will print an error or warning message to the console. If you used the drag-and-drop method, the window will stay open so you can read the message. Just press Enter to close it.

If you encounter an error, please **[create an issue](https://github.com/EastArctica/racelogic-ref-to-dbc/issues)** on the GitHub repository. If possible, please attach the `.ref` file that caused the problem, as this is extremely helpful for debugging.
