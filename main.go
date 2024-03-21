package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type FesterizeError int

// Exit codes used by the program
const (
	NO_FILES_SPECIFIED         FesterizeError = 1
	NONEXISTENT_FILE_SPECIFIED FesterizeError = 2
	NON_CSV_FILE_SPECIFIED     FesterizeError = 3
	FESTER_UNAVAILABLE         FesterizeError = 4
	FESTER_ERROR_RESPONSE      FesterizeError = 5
	FILE_IO_ERROR              FesterizeError = 6
	INVALID_OUTPUT_SPECIFIED   FesterizeError = 7
)

const (
	iiifApiHelp string = `IIIF Presentation API version that Fester should use.

Version 3 may be used for content intended to be viewed exclusively with
Mirador 3.

For all other cases, version 2 should be used, especially for any content
intended to be viewed with Universal Viewer.`

	strictModeHelp string = `Festerize immediately exits with an error code if Fester responds
with an error, or if a user specifies on the command line a file that does not
exist or a file that does not have a .csv filename extension. The rest of the
files on the command line (if any) will remain unprocessed.`

	festerizeMessage string = `Uploads CSV files to the Fester IIIF manifest service for processing.

Any rows with an 'Object Type' of 'Collection' (i.e., "collection row")
found in the CSV are used to create a IIIF collection.

Any rows with an 'Object Type' of 'Work' (i.e., "work row") are used to
expand or revise a previously created IIIF collection (corresponding to
the collection that the work is a part of), as well as create a IIIF
manifest corresponding to the work. A "work" is conceived of as a discrete
object (e.g., a book or a photograph) that one would access as an
individual item.

Any rows with an 'Object Type' of 'Page' (i.e., "page row") are likewise
used to expand or revise a previously created IIIF manifest (corresponding
to the work that the page is a part of), unless the '--metadata-update'
flag is used (in which case, page rows are ignored).

After Fester creates or updates any IIIF collections or manifests, it
updates and returns the CSV files to the user.

The returned CSVs are updated to contain URLs (in a 'IIIF Manifest URL'
column) of the IIIF collections and manifests that correspond to any
collection or work rows found in the CSV.

Note that the order of operations is important. The following will result
in an error:

	1. Running 'festerize' with a CSV containing works that are part of a
	collection for which no IIIF collection has been created (i.e., the
	work's corresponding collection hasn't been festerized yet)

		- Solution: add a collection row to the CSV and re-run 'festerize'
		with it, or run 'festerize' with another CSV that contains the
		collection row

	2. Running 'festerize' with a CSV containing pages that are part of a
	work for which no IIIF manifest has been created (i.e., the page's
	corresponding work hasn't been festerized yet)

		- Solution: add a work row to the CSV and re-run 'festerize' with
		it, or run 'festerize' with another CSV that contains the work row

Arguments:

	SRC is either a path to a CSV file or a Unix-style glob like '*.csv'.`
)

var iiifApiVersion string
var server string
var out string
var iiifhost string
var metadata bool
var strictMode bool
var loglevel string
var src []string
var Logger *zap.Logger = logger()
var festerizeVersion string = "0.4.2"

// Sets up Cobra command line
var rootCmd = &cobra.Command{
	Use:   "festerize [flags] [src]",
	Short: "A command-line tool for processing IIIF data.",
	Long:  festerizeMessage,
	Run: func(cmd *cobra.Command, args []string) {
		// Check if nothing was inputed
		if len(args) == 0 {
			cmd.Help()
			os.Exit(0)
		}

		if err := ValidateVersion(); err != nil {
			fmt.Println("IIIF API Version must be specified. Allowed values are 2 or 3")
			fmt.Println(iiifApiHelp)
			os.Exit(1)
		}

		if err := ValidateLoglevel(); err != nil {
			fmt.Println("Invalid log level. Allowed values are INFO, DEBUG, or ERROR.")
			os.Exit(1)
		}
		// Set loglevel for logger
		switch loglevel {
		case "INFO":
			Logger = Logger.WithOptions(zap.IncreaseLevel(zapcore.InfoLevel))
		case "DEBUG":
			Logger = Logger.WithOptions(zap.IncreaseLevel(zapcore.DebugLevel))
		case "ERROR":
			Logger = Logger.WithOptions(zap.IncreaseLevel(zapcore.ErrorLevel))
		default:
			Logger = Logger.WithOptions(zap.IncreaseLevel(zapcore.InfoLevel))
		}

		if len(args) == 0 {
			fmt.Println("Please provide one or more CSV files")
			os.Exit(int(NO_FILES_SPECIFIED))
		}
		src = append(src, args...)
	},
}

// ValidateLogLevel validates the log level
func ValidateLoglevel() error {
	switch loglevel {
	case "INFO", "DEBUG", "ERROR":
		return nil
	default:
		return errors.New("invalid log level. Allowed values are INFO, DEBUG, or ERROR")
	}
}

// ValidateVersion validates version number
func ValidateVersion() error {
	switch iiifApiVersion {
	case "2", "3":
		return nil
	default:
		return errors.New("IIIF API Version must be specified. Allowed values are 2 or 3")
	}
}

// ApplyExitOnHelp exits out of program if --help is flag
func ApplyExitOnHelp(c *cobra.Command, exitCode int) {
	helpFunc := c.HelpFunc()
	c.SetHelpFunc(func(c *cobra.Command, s []string) {
		helpFunc(c, s)
		os.Exit(exitCode)
	})
}

// logger creates logger
func logger() *zap.Logger {
	logger := zap.Must(zap.NewDevelopment())
	defer logger.Sync()
	return logger
}

// CreateOuputDir creates output directory
func CreateOutputDir() error {
	if _, err := os.Stat(out); os.IsNotExist(err) {
		fmt.Printf("Output directory %s not found, creating it.\n", out)
		if err := os.MkdirAll(out, os.ModePerm); err != nil {
			return errors.New("error creating output directory")
		}
	} else {
		fmt.Printf("Output directory %s found, should we continue? YES might overwrite any existing output files. (yes/no): ", out)
		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			return errors.New("aborted")
		}
	}
	return nil
}

// FesterStatus checks Fester availability
func FesterStatus(getStatusURL string) (int, error) {
	// If Fester is unavailable, abort
	resp, err := http.Get(getStatusURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, errors.New("error connecting to Fester: Unexpected status code")
	} else {
		return resp.StatusCode, nil
	}
}

// uploadCSV uploads csv to Fester and returns respone
func uploadCSV(filePath, postURL, iiifAPIVersion, iiifHost string,
	metadataUpdate bool, headers map[string]string) (*http.Response, []byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file field to the request
	part, err := writer.CreateFormFile("file", filePath)
	if err != nil {
		return nil, nil, err
	}

	// Copy the file content into the form field
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, nil, err
	}

	// Add other fields to the request payload
	writer.WriteField("iiif-version", "v"+iiifAPIVersion)
	if iiifHost != "" {
		writer.WriteField("iiif-host", iiifHost)
	}
	if metadataUpdate {
		writer.WriteField("metadata-update", "true")
	}

	// Close the multipart writer
	err = writer.Close()
	if err != nil {
		return nil, nil, err
	}

	// Create a POST request with the file upload
	request, err := http.NewRequest("POST", postURL, body)
	if err != nil {
		return nil, nil, err
	}

	// Set the content type for the request
	request.Header.Set("Content-Type", writer.FormDataContentType())

	// Add custom headers to the request
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	// Make the request
	client := &http.Client{}

	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}

	// Create a copy of the response body
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, err
	}

	defer response.Body.Close()

	return response, responseBody, nil
}

// init initates flags
func init() {
	// Flags
	rootCmd.Flags().StringVarP(&iiifApiVersion, "iiif-api-version", "v", "", iiifApiHelp)
	rootCmd.Flags().StringVarP(&server, "server", "", "https://test.ingest.iiif.library.ucla.edu", "URL of the Fester service dedicated for ingest")
	rootCmd.Flags().StringVarP(&out, "out", "", "output", "Local directory to put the updated CSV")
	rootCmd.Flags().StringVarP(&iiifhost, "iiifhost", "", "", "IIIF image server URL (optional)")
	rootCmd.Flags().BoolVarP(&metadata, "metadata-update", "m", false, "Only update manifest (work) metadata; don't update canvases (pages).")
	rootCmd.Flags().BoolVarP(&strictMode, "strict-mode", "", false, strictModeHelp)
	rootCmd.Flags().StringVarP(&loglevel, "loglevel", "", "INFO", "Log level (INFO, DEBUG, ERROR)")
}

func main() {
	ApplyExitOnHelp(rootCmd, 0)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Create output directory
	if err := CreateOutputDir(); err != nil {
		Logger.Error("Error creating output directory",
			zap.Error(err))
		os.Exit(int(INVALID_OUTPUT_SPECIFIED))
	}

	// HTTP request URLs.
	getStatusURL := server + "/fester/status"
	postCSVUrl := server + "/collections"

	// HTTP request headers
	requestHeaders := map[string]string{
		"User-Agent": fmt.Sprintf("%s/%s", "Festerize", festerizeVersion),
	}

	// Check if Fester is available
	if statusCode, err := FesterStatus(getStatusURL); err != nil {
		if statusCode != 0 {
			Logger.Error("Error connecting to Fester: Unexpected status code",
				zap.Int("status_code", statusCode),
			)
		} else {
			Logger.Error("Error making HTTP request to Fester",
				zap.Error(err),
			)
		}
		os.Exit(int(FESTER_UNAVAILABLE))
	} else {
		Logger.Info("Got valid status code connected to Fester",
			zap.Int("status_code", statusCode),
		)
	}

	for _, pathString := range src {
		// Convert the path string to an absolute path
		absPath, err := filepath.Abs(pathString)
		filename := filepath.Base(absPath)
		if err != nil {
			log.Fatal("Error getting absolute path", err)
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			Logger.Error("File does not exist",
				zap.String("filename", filename),
				zap.Error(err),
			)

			if strictMode {
				os.Exit(int(NONEXISTENT_FILE_SPECIFIED))
			}
		} else if strings.EqualFold(filepath.Ext(filename), ".csv") {
			fmt.Printf("Uploading %s to %s\n", filename, postCSVUrl)
			response, responseBody, err := uploadCSV(absPath, postCSVUrl, iiifApiVersion, iiifhost, metadata, requestHeaders)
			if response.StatusCode == 201 {
				fmt.Printf("%s was uploaded succesfully\n", filename)
				Logger.Info("File was uploaded to Fester succesfully",
					zap.String("filename", filename),
				)

				// Save the result CSV to the output directory
				csvPath := filepath.Join(out, filename)

				file, err := os.Create(csvPath)
				if err != nil {
					Logger.Error("Error creating file", zap.Error(err))
					if strictMode {
						os.Exit(int(FILE_IO_ERROR))
					}
				}
				defer file.Close()

				_, err = file.Write(responseBody)
				if err != nil {
					Logger.Error("Error writing to file", zap.Error(err))
					if strictMode {
						os.Exit(int(FILE_IO_ERROR))
					}
				} else {
					extraSatisfaction := []string{"🎉", "🎊", "✨", "💯", "😎", "✔️ ", "👍"} // Add more awesome characters if needed

					// Create a string of emojis repeated
					borderChar := extraSatisfaction[rand.Intn(len(extraSatisfaction))]
					message := "SUCCESS! Uploaded " + filename
					numSatisfaction := len(message)/2 + 3
					fmt.Println(strings.Repeat(borderChar, numSatisfaction))
					fmt.Println(borderChar, message, borderChar)
					fmt.Println(strings.Repeat(borderChar, numSatisfaction))

				}
			} else {
				if err != nil {
					Logger.Error("There was an error creating and posting the request: ", zap.Error(err))
				}

				doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(responseBody)))
				if err != nil {
					fmt.Printf("Failed to parse HTML: %v\n", err)
					return
				}

				// Log error response
				errorCause := doc.Find("#error-message").Text()
				Logger.Error("Failed to upload file to Fester",
					zap.String("filename", filename),
					zap.String("error", errorCause))
				if strictMode {
					os.Exit(int(FESTER_ERROR_RESPONSE))
				}
			}
		} else {
			Logger.Error("This file is not a CSV file")
			if strictMode {
				os.Exit(int(NON_CSV_FILE_SPECIFIED))
			}
		}
	}

}
