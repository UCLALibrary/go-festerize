package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type FesterizeError int

// Exit codes used by the program
const (
	NoFilesSpecified         FesterizeError = 1
	NonExistentFileSpecified FesterizeError = 2
	NonCsvFileSpecified      FesterizeError = 3
	FesterUnavailable        FesterizeError = 4
	FesterErrorResponse      FesterizeError = 5
	FileIoError              FesterizeError = 6
	InvalidOutputSpecified   FesterizeError = 7
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
var thumbnail bool
var strictMode bool
var loglevel string
var src []string
var Logger = logger()
var festerizeVersion = "0.5.0"
var logFile = "logs.log"

// Sets up Cobra command line.
var rootCmd = &cobra.Command{
	Use:   "festerize [flags] [src]",
	Short: "A command-line tool for processing IIIF data.",
	Long:  festerizeMessage,
	Run: func(cmd *cobra.Command, args []string) {
		// Check if no arguments were passed
		if len(args) == 0 {
			_ = cmd.Help()
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
			os.Exit(int(NoFilesSpecified))
		}
		src = append(src, args...)
	},
}

// ValidateLoglevel validates the log level.
func ValidateLoglevel() error {
	switch loglevel {
	case "INFO", "DEBUG", "ERROR":
		return nil
	default:
		return errors.New("invalid log level. Allowed values are INFO, DEBUG, or ERROR")
	}
}

// ValidateVersion validates version number.
func ValidateVersion() error {
	switch iiifApiVersion {
	case "2", "3":
		return nil
	default:
		return errors.New("IIIF API Version must be specified. Allowed values are 2 or 3")
	}
}

// ApplyExitOnHelp exits out of program if `-help` is a flag.
func ApplyExitOnHelp(cmd *cobra.Command, exitCode int) {
	helpFunc := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, str []string) {
		helpFunc(c, str)
		os.Exit(exitCode)
	})
}

// logger creates a Zap Logger with output of info and debug to file and error to stdout.
func logger() *zap.Logger {
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	fileEncoder := zapcore.NewJSONEncoder(encoderConfig)
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder // The encoder can be customized for each output

	// Create file core
	file, err := os.Create(logFile)
	if err != nil {
		panic(err)
	}

	fileCore := zapcore.NewCore(fileEncoder, zapcore.AddSync(file), zap.DebugLevel)

	// Create a logger with two cores
	logger := zap.New(zapcore.NewTee(fileCore), zap.AddCaller())

	return logger
}

// CreateOutputDir creates output directory.
func CreateOutputDir() error {
	if _, err := os.Stat(out); os.IsNotExist(err) {
		fmt.Printf("Output directory %s not found, creating it.\n", out)
		if err := os.MkdirAll(out, os.ModePerm); err != nil {
			return errors.New("error creating output directory")
		}
	} else {
		fmt.Printf("Output dir '%s' found, should we continue? Yes will overwrite any existing files. (Y/n): ", out)
		var response string
		_, _ = fmt.Scanln(&response)
		if strings.ToLower(response) != "yes" && strings.ToLower(response) != "y" && response != "" {
			return errors.New("aborted")
		}
	}
	return nil
}

// uploadCSV uploads csv to Fester and returns response.
func uploadCSV(filePath, postURL, iiifAPIVersion, iiifHost string,
	metadataUpdate bool, headers map[string]string) (*http.Response, []byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

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
	_ = writer.WriteField("iiif-version", "v"+iiifAPIVersion)
	if iiifHost != "" {
		_ = writer.WriteField("iiif-host", iiifHost)
	}
	if metadataUpdate {
		_ = writer.WriteField("metadata-update", "true")
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

	// Set Basic Auth if we have that information
	if err = godotenv.Load(); err != nil { // Defaults to ".env" in the current directory
		Logger.Debug("No .env file was found; u/p should be set in the system ENV")
	}

	// Check that username and password were found in the .env file
	username := os.Getenv("FESTERIZE_USERNAME")
	password := os.Getenv("FESTERIZE_PASSWORD")
	if username == "" {
		return nil, nil, fmt.Errorf("basic auth username was not found")
	}
	if password == "" {
		return nil, nil, errors.New("basic auth password was not found")
	}

	// Set that basic auth information
	request.SetBasicAuth(username, password)

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
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(response.Body)

	return response, responseBody, nil
}

// init initiates flags for command line arguments.
func init() {
	// Flags
	rootCmd.Flags().StringVarP(&iiifApiVersion, "iiif-api-version", "v", "3", iiifApiHelp)
	rootCmd.Flags().StringVarP(&server, "server", "", "https://ingest.iiif.library.ucla.edu", "URL of the Fester service dedicated for ingest")
	rootCmd.Flags().StringVarP(&out, "out", "", "output", "Local directory to put the updated CSV")
	rootCmd.Flags().StringVarP(&iiifhost, "iiifhost", "", "", "IIIF image server URL (optional)")
	rootCmd.Flags().BoolVarP(&metadata, "metadata-update", "m", false, "Only update manifest (work) metadata; don't update canvases (pages).")
	rootCmd.Flags().BoolVarP(&thumbnail, "thumbnails", "t", false, "Add a thumbnail to each work in a collection")
	rootCmd.Flags().BoolVarP(&strictMode, "strict-mode", "", false, strictModeHelp)
	rootCmd.Flags().StringVarP(&loglevel, "loglevel", "", "INFO", "Log level (INFO, DEBUG, ERROR)")
}

// main runs the festerize program.
func main() {
	ApplyExitOnHelp(rootCmd, 0)
	if err := rootCmd.Execute(); err != nil {
		Logger.Error("Error setting command line",
			zap.Error(err))
		fmt.Println("There was an error setting the command line")
		os.Exit(1)
	}

	// Create output directory
	if err := CreateOutputDir(); err != nil {
		Logger.Error("Error creating output directory",
			zap.Error(err))
		fmt.Println("There was an error creating an output directory")
		os.Exit(int(InvalidOutputSpecified))
	}

	// HTTP request URLs
	postCSVUrl := server + "/collections"
	postThumbUrl := server + "/thumbnails"

	// HTTP request headers
	requestHeaders := map[string]string{
		"User-Agent": fmt.Sprintf("%s/%s", "Festerize", festerizeVersion),
	}

	for _, pathString := range src {
		// Convert the path string to an absolute path
		absPath, err := filepath.Abs(pathString)
		filename := filepath.Base(absPath)
		if err != nil {
			Logger.Error("Error getting absolute path",
				zap.Error(err))
			fmt.Println("There was an error getting the absolute path of the CSV")
			if strictMode {
				os.Exit(int(FileIoError))
			}
			continue
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			Logger.Error("File does not exist",
				zap.String("filename", filename),
				zap.Error(err),
			)
			fmt.Printf("%s does not exist\n", filename)
			if strictMode {
				os.Exit(int(NonExistentFileSpecified))
			}
		} else if strings.EqualFold(filepath.Ext(filename), ".csv") {
			var uploadUrl string
			if thumbnail {
				uploadUrl = postThumbUrl
			} else {
				uploadUrl = postCSVUrl
			}
			Logger.Info("Uploading file to Fester",
				zap.String("filename", filename),
				zap.String("upload URL", uploadUrl))
			response, body, err := uploadCSV(absPath, uploadUrl, iiifApiVersion, iiifhost, metadata, requestHeaders)
			if err == nil && response.StatusCode == 201 {
				Logger.Info("File was uploaded to Fester successfully",
					zap.String("filename", filename),
				)

				// Save the result CSV to the output directory
				csvPath := filepath.Join(out, filename)

				file, err := os.Create(csvPath)
				if err != nil {
					Logger.Error("Error creating file", zap.Error(err))
					fmt.Printf("There was an error creating the festerized version of %s\n", filename)
					if strictMode {
						os.Exit(int(FileIoError))
					}
					continue
				}
				defer func(file *os.File) {
					_ = file.Close()
				}(file)

				_, err = file.Write(body)
				if err != nil {
					Logger.Error("Error writing to file", zap.Error(err))
					fmt.Printf("There was an error writing to %s\n", filename)
					if strictMode {
						os.Exit(int(FileIoError))
					}
					continue
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
					fmt.Printf("Check log. There was an error while attempting to upload: %s\n", filename)
				}

				doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
				if err != nil {
					Logger.Error("Failed to parse error HTML",
						zap.Error(err))
					continue
				}

				// Log error response with additional information, if any
				if errorCause := doc.Find("#error-message").Text(); errorCause != "" {
					Logger.Error("Failed to upload file to Fester",
						zap.String("filename", filename),
						zap.String("error", errorCause))
				}

				if strictMode {
					os.Exit(int(FesterErrorResponse))
				}
			}
		} else {
			Logger.Error("This file is not a CSV file",
				zap.String("filename", filename))
			fmt.Printf("%s is not a CSV \n", filename)
			if strictMode {
				os.Exit(int(NonCsvFileSpecified))
			}
		}
	}
}
