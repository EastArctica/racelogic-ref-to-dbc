package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Signal represents a single signal within a CAN message.
type Signal struct {
	Name      string
	StartBit  int
	Length    int
	ByteOrder byte // 0 for Motorola (big-endian), 1 for Intel (little-endian)
	IsSigned  bool
	Factor    float64
	Offset    float64
	Min       float64
	Max       float64
	Unit      string
}

// Message represents a CAN message, containing one or more signals.
type Message struct {
	ID      uint32
	Name    string
	DLC     int
	Node    string
	Signals []*Signal
}

// main is the entry point for the program. It handles command-line arguments,
// file I/O, and orchestrates the parsing process for multiple files.
func main() {
	// Define command-line flags for input and output files.
	inputFileFlag := flag.String("i", "", "Input file path. Can be used with positional arguments.")
	outputFileFlag := flag.String("o", "", "Output file path. (Only used when a single input file is provided)")
	flag.Parse()

	// Collect all input files from both the -i flag and positional arguments.
	inputFiles := []string{}
	if *inputFileFlag != "" {
		inputFiles = append(inputFiles, *inputFileFlag)
	}
	inputFiles = append(inputFiles, flag.Args()...)

	// If no files are provided, show usage and exit.
	if len(inputFiles) == 0 {
		fmt.Println("Error: No input file specified.")
		fmt.Println("Usage: racelogic-ref-to-dbc [options] <file1> <file2> ...")
		fmt.Println("Options:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Warn user if -o is used with multiple files, as it will be ignored.
	if len(inputFiles) > 1 && *outputFileFlag != "" {
		fmt.Println("Warning: -o flag is ignored when more than one input file is provided.")
	}

	var hadAnyIssues bool
	var filesProcessed int

	// Process each file provided.
	for _, currentInput := range inputFiles {
		fmt.Printf("\n--- Processing file: %s ---\n", currentInput)

		var currentOutput string
		// Determine output path. Use -o only if one file is being processed.
		if len(inputFiles) == 1 && *outputFileFlag != "" {
			currentOutput = *outputFileFlag
		} else {
			ext := filepath.Ext(currentInput)
			baseName := strings.TrimSuffix(filepath.Base(currentInput), ext)
			currentOutput = filepath.Join(filepath.Dir(currentInput), baseName+".dbc")
		}
		fmt.Printf("Output will be written to: %s\n", currentOutput)

		hasWarnings, err := processFile(currentInput, currentOutput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR processing %s: %v\n", currentInput, err)
			hadAnyIssues = true
			continue // Move to the next file
		}
		if hasWarnings {
			hadAnyIssues = true
		}
		filesProcessed++
	}

	fmt.Printf("\n--- Finished ---\n")
	fmt.Printf("Successfully processed %d out of %d file(s).\n", filesProcessed, len(inputFiles))

	// If any error or warning occurred during the entire run, pause for user to see.
	if hadAnyIssues {
		fmt.Println("\nNOTE: Errors or warnings were issued during processing (see details above).")
		fmt.Println("Press Enter to exit.")
		bufio.NewReader(os.Stdin).ReadBytes('\n')
	}
}

// processFile handles the opening, parsing, and writing of the data for a single file.
// It returns a boolean indicating if any warnings occurred, and an error for fatal issues.
func processFile(inputPath, outputPath string) (bool, error) {
	var hasWarnings bool

	file, err := os.Open(inputPath)
	if err != nil {
		return hasWarnings, fmt.Errorf("failed to open input file: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	// --- PARSING LOGIC BASED ON THE .hexpat STRUCTURE ---

	// 1. Skip headers
	_, err = readUpToCRLF(reader) // Header
	if err != nil {
		return hasWarnings, fmt.Errorf("failed to read header: %w", err)
	}
	if _, err := reader.Discard(2); err != nil {
		return hasWarnings, fmt.Errorf("failed to discard header delimiter: %w", err)
	}
	_, err = readUpToCRLF(reader) // Serial String
	if err != nil {
		return hasWarnings, fmt.Errorf("failed to read serial string: %w", err)
	}
	if _, err := reader.Discard(2); err != nil {
		return hasWarnings, fmt.Errorf("failed to discard serial string delimiter: %w", err)
	}
	if _, err := readZlibStr(reader); err != nil { // Zlib Serial
		return hasWarnings, fmt.Errorf("failed to read zlib serial block: %w", err)
	}

	// 2. Read total entries
	var totalEntries uint16
	if err := binary.Read(reader, binary.BigEndian, &totalEntries); err != nil {
		return hasWarnings, fmt.Errorf("failed to read total entries count: %w", err)
	}
	fmt.Printf("Found %d entries to process.\n", totalEntries)

	// 3. Decompress all entries into a list of strings
	var allLines []string
	for i := uint16(0); i < totalEntries; i++ {
		compressedData, err := readZlibStr(reader)
		if err != nil {
			return hasWarnings, fmt.Errorf("failed to read entry #%d: %w", i+1, err)
		}
		decompressedData, err := decompressZlib(compressedData)
		if err != nil {
			// Log non-critical decompression errors and continue
			fmt.Fprintf(os.Stderr, "Warning: could not decompress entry #%d: %v\n", i+1, err)
			hasWarnings = true
			continue
		}
		// The decompressed data can contain multiple lines, so we scan it
		scanner := bufio.NewScanner(bytes.NewReader(decompressedData))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) != "" {
				allLines = append(allLines, line)
			}
		}
	}

	// 4. Check for any remaining unparsed data at the end of the file.
	_, err = reader.ReadByte()
	if err == nil {
		// If we successfully read a byte, it means there's extra data.
		fmt.Println("Warning: The file was processed, but there is unparsed data remaining at the end of the file.")
		hasWarnings = true
		// We don't need to do anything with the extra data, just notify the user.
	} else if err != io.EOF {
		// An error other than EOF occurred while checking, which is unexpected.
		return hasWarnings, fmt.Errorf("error while checking for remaining data: %w", err)
	}
	// If err is io.EOF, we've read the file perfectly.

	// 5. Parse the collected lines into structured Message and Signal data
	messages, parseWarnings, err := parseSignalLines(allLines)
	hasWarnings = hasWarnings || parseWarnings // Combine warnings from this function and the parser.
	if err != nil {
		return hasWarnings, fmt.Errorf("failed to parse signal data: %w", err)
	}

	// 6. Write the structured data to the output file in DBC format
	outFile, err := os.Create(outputPath)
	if err != nil {
		return hasWarnings, fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	if err := writeDBC(messages, writer); err != nil {
		return hasWarnings, fmt.Errorf("failed to write DBC file: %w", err)
	}
	return hasWarnings, writer.Flush()
}

// parseSignalLines converts the raw CSV-like lines into a map of structured Messages.
// It returns the messages, a boolean indicating if warnings occurred, and an error.
func parseSignalLines(lines []string) (map[uint32]*Message, bool, error) {
	var hasWarnings bool
	messages := make(map[uint32]*Message)
	defaultNode := "VECTOR__XXX"

	for i, line := range lines {
		// Clean up trailing commas and split
		parts := strings.Split(strings.Trim(line, " \t,"), ",")
		if len(parts) < 11 {
			fmt.Fprintf(os.Stderr, "Warning: skipping malformed line #%d (not enough fields): %s\n", i+1, line)
			hasWarnings = true
			continue
		}

		// Parse all parts, converting to correct types
		msgID, err := strconv.ParseUint(parts[1], 10, 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping line #%d (invalid message ID): %s\n", i+1, line)
			hasWarnings = true
			continue
		}

		startBit, _ := strconv.Atoi(parts[3])
		length, _ := strconv.Atoi(parts[4])
		offset, _ := strconv.ParseFloat(parts[5], 64)
		factor, _ := strconv.ParseFloat(parts[6], 64)
		max, _ := strconv.ParseFloat(parts[7], 64)
		min, _ := strconv.ParseFloat(parts[8], 64)
		isSigned := strings.ToLower(parts[9]) == "signed"
		var byteOrder byte = 0 // Default to Motorola (big-endian)
		if strings.ToLower(parts[10]) == "intel" {
			byteOrder = 1 // Intel (little-endian)
		}

		var dlc int
		if len(parts) >= 12 {
			dlc, err = strconv.Atoi(parts[11])
			if err != nil {
				// If DLC is present but not a valid number, warn and default to 8.
				fmt.Fprintf(os.Stderr, "Warning: line #%d has invalid DLC '%s', assuming 8. Line: %s\n", i+1, parts[11], line)
				hasWarnings = true
				dlc = 8
			}
		} else {
			// DLC is missing, assume default of 8 and notify user.
			fmt.Fprintf(os.Stderr, "Info: line #%d is missing DLC field, assuming default of 8.\n", i+1)
			hasWarnings = true
			dlc = 8
		}

		// If message doesn't exist in our map, create it
		if _, ok := messages[uint32(msgID)]; !ok {
			messages[uint32(msgID)] = &Message{
				ID:   uint32(msgID),
				Name: fmt.Sprintf("CAN_MSG_%d", msgID),
				DLC:  dlc,
				Node: defaultNode,
			}
		} else {
			// If message already exists, ensure DLC is consistent.
			// A larger DLC might be found on a later signal for the same message.
			if dlc > messages[uint32(msgID)].DLC {
				messages[uint32(msgID)].DLC = dlc
			}
		}

		// Create the signal
		signal := &Signal{
			Name:      parts[0],
			Unit:      parts[2],
			StartBit:  startBit,
			Length:    length,
			Offset:    offset,
			Factor:    factor,
			Max:       max,
			Min:       min,
			IsSigned:  isSigned,
			ByteOrder: byteOrder,
		}

		// Add signal to its parent message
		messages[uint32(msgID)].Signals = append(messages[uint32(msgID)].Signals, signal)
	}
	return messages, hasWarnings, nil
}

// writeDBC formats the structured message map into a valid DBC file.
func writeDBC(messages map[uint32]*Message, w *bufio.Writer) error {
	// Write DBC Header
	w.WriteString("VERSION \"\"\n\n")
	w.WriteString("NS_ :\n\tCM_\n\tBA_DEF_\n\tBA_\n\tVAL_\n\tCAT_DEF_\n\tCAT_\n\tFILTER\n\tBA_DEF_DEF_\n\tEV_DATA_\n\tENVVAR_DATA_\n\tSGTYPE_\n\tSGTYPE_VAL_\n\tBA_DEF_SGTYPE_\n\tBA_SGTYPE_\n\tSIG_TYPE_REF_\n\tVAL_TABLE_\n\tSIG_GROUP_\n\tSIG_VALTYPE_\n\tSIGTYPE_VALTYPE_\n\tBO_TX_BU_\n\tBA_DEF_REL_\n\tBA_REL_\n\tBA_DEF_DEF_REL_\n\tBU_SG_REL_\n\tBU_EV_REL_\n\tBU_BO_REL_\n\tSG_MUL_VAL_\n")
	w.WriteString("\nBS_:\n\n")

	// Write Nodes
	defaultNode := "VECTOR__XXX"
	w.WriteString(fmt.Sprintf("BU_: %s\n\n", defaultNode))

	// Get and sort message IDs for consistent output order
	ids := make([]uint32, 0, len(messages))
	for id := range messages {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// Write all Messages (BO_) and their Signals (SG_)
	for _, id := range ids {
		msg := messages[id]
		fmt.Fprintf(w, "BO_ %d %s: %d %s\n", msg.ID, msg.Name, msg.DLC, msg.Node)
		for _, sig := range msg.Signals {
			byteOrderChar := '0' // @0 for Motorola
			if sig.ByteOrder == 1 {
				byteOrderChar = '1' // @1 for Intel
			}

			signChar := '+' // unsigned
			if sig.IsSigned {
				signChar = '-' // signed
			}

			fmt.Fprintf(w, " SG_ %s : %d|%d@%c%c (%g,%g) [%g|%g] \"%s\" %s\n",
				sig.Name,
				sig.StartBit,
				sig.Length,
				byteOrderChar,
				signChar,
				sig.Factor,
				sig.Offset,
				sig.Min,
				sig.Max,
				sig.Unit,
				defaultNode,
			)
		}
		w.WriteString("\n")
	}

	return nil
}

// --- UTILITY FUNCTIONS (Unchanged) ---

func readUpToCRLF(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		peekedBytes, err := r.Peek(2)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				remaining, readErr := io.ReadAll(r)
				return append(line, remaining...), readErr
			}
			return nil, err
		}
		if peekedBytes[0] == '\r' && peekedBytes[1] == '\n' {
			return line, nil
		}
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		line = append(line, b)
	}
}

func readZlibStr(r io.Reader) ([]byte, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("could not read zlib string length: %w", err)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("could not read zlib string data (expected %d bytes): %w", length, err)
	}
	return data, nil
}

func decompressZlib(compressedData []byte) ([]byte, error) {
	byteReader := bytes.NewReader(compressedData)
	zlibReader, err := zlib.NewReader(byteReader)
	if err != nil {
		return nil, err
	}
	defer zlibReader.Close()
	return io.ReadAll(zlibReader)
}
