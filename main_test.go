// main_test.go
package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var userInputMutex sync.Mutex
var TestOutputDir string = "test/test-resources/test_output_dir"
var TestDirUnFester string = "test/test-resources/un-festerized"
var TestDirFester string = "test/test-resources/festerized"
var TestDirThumb string = "test/test-resources/thumbnails"

// MemorySink implements zap.Sink by writing all messages to a buffer.
type MemorySink struct {
	*bytes.Buffer
}

// Implement Close and Sync as no-ops to satisfy the interface. The Write
// method is provided by the embedded buffer.
func (s *MemorySink) Close() error { return nil }
func (s *MemorySink) Sync() error  { return nil }

// createLogger creates a test logger to be used
func createLogger() (Logger *zap.Logger, sink *MemorySink) {
	// Create a sink instance, and register it with zap for the "memory"
	// protocol.
	sink = &MemorySink{new(bytes.Buffer)}
	zap.RegisterSink("memory", func(*url.URL) (zap.Sink, error) {
		return sink, nil
	})

	// Create a logger instance using the registered sink.
	Logger = zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.AddSync(sink),
		zapcore.DebugLevel,
	))
	return Logger, sink
}

// simulateUserInput simulates stdin input during testing
func simulateUserInput(input string) {
	userInputMutex.Lock()
	defer userInputMutex.Unlock()

	reader, writer, _ := os.Pipe()
	go func() {
		defer writer.Close()
		io.WriteString(writer, input)
	}()

	os.Stdin = reader
}

// redirectStdoutToBuffer redirects the standard out so that it is not seen when running test
func redirectStdoutToBuffer(t *testing.T) *bytes.Buffer {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	returnedBuffer := new(bytes.Buffer)

	go func() {
		defer func() {
			_ = r.Close()
			os.Stdout = oldStdout
		}()

		io.Copy(returnedBuffer, r)
	}()

	// Restore os.Stdout when the test ends
	t.Cleanup(func() {
		_ = r.Close()
		os.Stdout = oldStdout
	})

	return returnedBuffer
}

// compareCSVs compares two CSV files and returns true if they are identical, false otherwise.
func compareCSVs(file1, file2 string, fullCompare bool) (bool, error) {
	// Open the first CSV file
	f1, err := os.Open(file1)
	if err != nil {
		return false, err
	}
	defer f1.Close()

	// Open the second CSV file
	f2, err := os.Open(file2)
	if err != nil {
		return false, err
	}
	defer f2.Close()

	// Create CSV readers for both files
	reader1 := csv.NewReader(f1)
	reader2 := csv.NewReader(f2)

	// Compare row by row
	for {
		row1, err1 := reader1.Read()
		row2, err2 := reader2.Read()

		// Check for EOF
		if err1 != nil && err2 != nil {
			if err1 == err2 {
				return true, nil // Files are identical
			}
			return false, fmt.Errorf("error comparing files: %v, %v", err1, err2)
		}

		// Check if number of columns match
		if len(row1) != len(row2) {
			return false, nil // Files have different structure
		}

		if fullCompare {
			// Compare each column
			for i := range row1 {
				if row1[i] != row2[i] {
					return false, nil // Files have different content
				}
			}
		}
	}
}

// TestValidateLogLevel tests loglevels
func TestValidateLoglevel(t *testing.T) {
	tests := []struct {
		loglevel string
		wantErr  bool
	}{
		{"INFO", false},
		{"DEBUG", false},
		{"ERROR", false},
		{"INVALID", true},
	}

	for _, tt := range tests {
		t.Run(tt.loglevel, func(t *testing.T) {
			loglevel = tt.loglevel
			err := ValidateLoglevel()

			if tt.wantErr && err == nil {
				t.Error("Expected an error, but got none.")
			} else if !tt.wantErr && err != nil {
				t.Error("Unexpected error:", err)
			}
		})
	}

}

// TestValidateVersion tests versions are properly validated
func TestValidateVersion(t *testing.T) {
	tests := []struct {
		version string
		wantErr bool
	}{
		{"2", false},
		{"3", false},
		{"4", true},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			iiifApiVersion = tt.version
			err := ValidateVersion()

			if tt.wantErr && err == nil {
				t.Error("Expected an error, but got none.")
			} else if !tt.wantErr && err != nil {
				t.Error("Unexpected error:", err)
			}
		})
	}
}

// TestCreateOutputDir tests the creation of an output directory given valid and invalid inputs
func TestCreateOutputDir(t *testing.T) {
	_ = redirectStdoutToBuffer(t)

	testCases := []struct {
		name          string
		out           string
		userInput     string
		expectedError error
	}{
		{
			name:          "Output directory does not exist",
			out:           TestOutputDir,
			userInput:     "",
			expectedError: nil,
		},
		{
			name:          "Output directory already exists, user enters 'yes'",
			out:           TestOutputDir,
			userInput:     "yes\n",
			expectedError: nil,
		},
		{
			name:          "Output directory already exists, user enters 'no'",
			out:           TestOutputDir,
			userInput:     "no\n",
			expectedError: errors.New("aborted"),
		},
	}
	for _, tc := range testCases {
		out = tc.out
		// Use the helper function to simulate user input during testing
		simulateUserInput(tc.userInput)
		// Call the function being tested
		err := CreateOutputDir()

		// Clean up the created directory
		defer os.RemoveAll(tc.out)

		// Check the result against the expected error
		if (err != nil && tc.expectedError == nil) || (err == nil && tc.expectedError != nil) || (err != nil && err.Error() != tc.expectedError.Error()) {
			t.Errorf("[%s] Test failed. Test failed onExpected error: %v, got: %v", tc.name, tc.expectedError, err)
		}
	}
}

// TestUploadCSV tests if a CSV is properly uploaded otherwise an error should be thrown and
func TestUploadCSV(t *testing.T) {
	// Valid File
	testDirectory := "test/test-resources"
	testDirUnFester := "test/test-resources/un-festerized/"
	testDirFester := "test/test-resources/festerized/"

	tests := []struct {
		fileName               string
		verifiedFesterizedpath string
		postURL                string
		iiifAPIVersion         string
		iiifHost               string
		metadataUpdate         bool
		headers                map[string]string
		expectedError          error
		expStatusCode          int
	}{
		{
			fileName:       "ballin.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/collections",
			iiifAPIVersion: "2",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 201,
		},
		{
			fileName:       "chandler.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/collections",
			iiifAPIVersion: "2",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 201,
		},
		{
			fileName:       "chase.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/collections",
			iiifAPIVersion: "2",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 201,
		},
		{
			fileName:       "edson.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/collections",
			iiifAPIVersion: "2",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 201,
		},
	}

	for _, tc := range tests {
		t.Run(tc.fileName, func(t *testing.T) {
			filePath := testDirUnFester + tc.fileName
			response, responseBody, err := uploadCSV(filePath, tc.postURL, tc.iiifAPIVersion, tc.iiifHost,
				tc.metadataUpdate, tc.headers)
			assert.Equal(t, err, nil)
			assert.Equal(t, response.StatusCode, tc.expStatusCode)
			if response.StatusCode == 201 {
				tempDir, err := os.MkdirTemp(testDirectory, "temporary-")
				if err != nil {
					fmt.Println("Error creating temporary directory:", err)
					return
				}
				defer os.RemoveAll(tempDir) // Clean up the temporary directory when done
				festerizedPath := filepath.Join(tempDir, tc.fileName)
				file, _ := os.Create(festerizedPath)
				defer file.Close()

				_, _ = file.Write(responseBody)
				filePath = testDirFester + tc.fileName
				match, err := compareCSVs(festerizedPath, filePath, true)
				if err != nil {
					fmt.Println("Error:", err)
					return
				}

				if !match {
					fmt.Println("Files match.")
					t.Errorf("Festerized CSV did not contain expected values")
				}
			}
		})
	}
}

// TestThumbnailCSV tests if a CSV is properly updated with default thumbnail otherwise an error should be thrown
func TestThumbnailCSV(t *testing.T) {
	// Valid File
	testDirectory := "test/test-resources"
	testDirUnThumb := "test/test-resources/unthumbed/"
	testDirThumbed := "test/test-resources/thumbed/"

	tests := []struct {
		fileName               string
		verifiedFesterizedpath string
		postURL                string
		iiifAPIVersion         string
		iiifHost               string
		metadataUpdate         bool
		headers                map[string]string
		expectedError          error
		expStatusCode          int
	}{
		{
			fileName:       "aidsposters_works_complex.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/thumbnails",
			iiifAPIVersion: "3",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 200,
		},
		{
			fileName:       "aldine_work.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/thumbnails",
			iiifAPIVersion: "3",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 200,
		},
		{
			fileName:       "canonlaw_works.csv",
			postURL:        "https://test.ingest.iiif.library.ucla.edu/thumbnails",
			iiifAPIVersion: "3",
			iiifHost:       "",
			metadataUpdate: false,
			headers: map[string]string{
				"User-Agent": fmt.Sprintf("%s/%s", "Festerize", "0.4.2")},
			expectedError: nil,
			expStatusCode: 200,
		},
	}

	for _, tc := range tests {
		t.Run(tc.fileName, func(t *testing.T) {
			filePath := testDirUnThumb + tc.fileName
			response, responseBody, err := uploadCSV(filePath, tc.postURL, tc.iiifAPIVersion, tc.iiifHost,
				tc.metadataUpdate, tc.headers)
			assert.Equal(t, err, nil)
			assert.Equal(t, response.StatusCode, tc.expStatusCode)
			if response.StatusCode == 200 {
				tempDir, err := os.MkdirTemp(testDirectory, "temporary-")
				if err != nil {
					fmt.Println("Error creating temporary directory:", err)
					return
				}
				defer os.RemoveAll(tempDir) // Clean up the temporary directory when done
				thumbedPath := filepath.Join(tempDir, tc.fileName)
				file, _ := os.Create(thumbedPath)
				defer file.Close()

				_, _ = file.Write(responseBody)
				filePath = testDirThumbed + tc.fileName
				match, err := compareCSVs(thumbedPath, filePath, false)
				if err != nil {
					fmt.Println("Error:", err)
					return
				}

				if !match {
					fmt.Println("Files match.")
					t.Errorf("Thumbnailed CSV did not contain expected values")
				}
			}
		})
	}
}

// TestMainValid tests an instance where all inputs are valid to the program and a file should be processed fully
func TestMainValid(t *testing.T) {
	redirectStdoutToBuffer(t)

	// Create a logger instance using the registered sink.
	logger, sink := createLogger()
	defer logger.Sync()

	Logger = logger

	testCSV := "/ballin.csv"
	os.Args = []string{"cmd", "--iiif-api-version=2", "--out=" + TestOutputDir, "--loglevel=INFO", TestDirUnFester + testCSV}
	defer os.RemoveAll(TestOutputDir)
	simulateUserInput("yes")
	main()

	// Assert sink contents
	output := sink.String()
	// Verifies that file was uploaded successfully through the logger
	if !strings.Contains(output, `File was uploaded to Fester succesfully`) {
		t.Error("File should have been uploaded to Fester succesfully but an error occured")
	}

	match, err := compareCSVs(TestOutputDir+"output"+testCSV, TestDirFester+testCSV, true)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	if !match {
		fmt.Println("Files match.")
		t.Errorf("Festerized CSV did not contain expected values")
	}

}

// TestMainInvalidCSV tests an invalid CSV and gets a valid response
func TestMainInvalidCSV(t *testing.T) {
	redirectStdoutToBuffer(t)

	// Create a logger instance using the registered sink.
	logger, sink := createLogger()
	defer logger.Sync()

	Logger = logger

	testCSV := "/random.csv"
	os.Args = []string{"cmd", "--iiif-api-version=2", "--out=" + TestOutputDir, "--loglevel=INFO", testCSV}
	defer os.RemoveAll(TestOutputDir)
	simulateUserInput("yes")

	main()
	// Assert sink contents
	output := sink.String()
	if !strings.Contains(output, `File does not exist`) {
		t.Error("File should not exist")
	}

}

// TestInvalidFesterResponse tests an instance where Fester responds with a non 200 code
func TestInvalidFesterResponse(t *testing.T) {
	redirectStdoutToBuffer(t)

	// Create a logger instance using the registered sink.
	logger, sink := createLogger()
	defer logger.Sync()

	Logger = logger

	festerizeVersion = "0.0.1"
	testCSV := "/ballin.csv"
	os.Args = []string{"cmd", "--iiif-api-version=2", "--out=" + TestOutputDir, "--loglevel=INFO", TestDirUnFester + testCSV}
	defer os.RemoveAll(TestOutputDir)
	simulateUserInput("yes")
	main()

	// Assert sink contents
	output := sink.String()
	// Verifies that file was uploaded successfully through the logger
	// fmt.Println(output)
	if !strings.Contains(output, `Failed to upload file to Fester`) {
		t.Error("The file should have failed to upload to Fester")
	}

}
