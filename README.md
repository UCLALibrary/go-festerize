# Festerize

Uploads CSV files to the Fester IIIF manifest service for processing.

Any rows with an `Object Type` of `Collection` (i.e., "collection row") found in the CSV are used to create a IIIF collection.

Any rows with an `Object Type` of `Work` (i.e., "work row") are used to expand or revise a previously created IIIF collection (corresponding to the collection that the work is a part of), as well as create a IIIF manifest corresponding to the work. A "work" is conceived of as a discrete object (e.g., a book or a photograph) that one would access as an individual item.

Any rows with an `Object Type` of `Page` (i.e., "page row") are likewise used to expand or revise a previously created IIIF manifest (corresponding to the work that the page is a part of), unless the `--metadata-update` flag is used (in which case, page rows are ignored).

After Fester creates or updates any IIIF collections or manifests, it updates and returns the CSV files to the user.

The returned CSVs are updated to contain URLs (in a `IIIF Manifest URL` column) of the IIIF collections and manifests that correspond to any collection or work rows found in the CSV.

Note that the order of operations is important. The following will result in an error:

1. Running `festerize` with a CSV containing works that are part of a collection for which no IIIF collection has been created (i.e., the work's corresponding collection hasn't been festerized yet)

    - **Solution**: add a collection row to the CSV and re-run `festerize` with it, or run `festerize` with another CSV that contains the collection row

1. Running `festerize` with a CSV containing pages that are part of a work for which no IIIF manifest has been created (i.e., the page's corresponding work hasn't been festerized yet)

    - **Solution**: add a work row to the CSV and re-run `festerize` with it, or run `festerize` with another CSV that contains the work row

## Installation

First, ensure that you have Go Version 1.22 on you system. Clone this repository and run 

`go build -o festerize main.go`

## Usage

After it's installed, you can see the available options by running:

    ./festerize --help

When you do this, you should see the following:

```
Uploads CSV files to the Fester IIIF manifest service for processing.

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

	SRC is either a path to a CSV file or a Unix-style glob like '*.csv'.

Usage:
  festerize [flags] [src]

Flags:
  -h, --help                      help for festerize
  -v, --iiif-api-version string   IIIF Presentation API version that Fester should use.
                                  
                                  Version 3 may be used for content intended to be viewed exclusively with
                                  Mirador 3.
                                  
                                  For all other cases, version 2 should be used, especially for any content
                                  intended to be viewed with Universal Viewer.
      --iiifhost string           IIIF image server URL (optional)
      --loglevel string           Log level (INFO, DEBUG, ERROR) (default "INFO")
  -m, --metadata-update           Only update manifest (work) metadata; don't update canvases (pages).
      --out string                Local directory to put the updated CSV (default "output")
      --server string             URL of the Fester service dedicated for ingest (default "https://ingest.iiif.library.ucla.edu")
      --strict-mode               Festerize immediately exits with an error code if Fester responds
                                  with an error, or if a user specifies on the command line a file that does not
                                  exist or a file that does not have a .csv filename extension. The rest of the
                                  files on the command line (if any) will remain unprocessed.
```

Below is a table which lists whether the flag is necessary or optional and what the default values are set to. 

| Long Flag         | Short Flag    | Default                                      | Optional   |
| ----------------- |-------------- | -------------------------------------------- |----------- |
| --iiif-api-version| -v            | N/A                                          | No         |
| --iiifhost        | N/A           | N/A                                          | Yes        |
| --loglevel        | N/A           | INFO                                         | No         |
| --metadata-update | -m            | false                                        | No         |
| --out             | N/A           | output                                       | No         |
| --server          | N/A           | https://ingest.iiif.library.ucla.edu         | No         |
| --strict-mode     | N/A           | false                                        | No         |

For all flags that have a default value, the flag is not necessary unless you want to change the default value. Either the long or short flag can be used in the command. The only flag that absolutely needs to be set is the `--iiif-api-version`. The available versions are 2 or 3. 

Festerize creates a folder (by default called `output`) for all output. CSVs returned by the Fester service are stored there, with the same name as the SRC file. 

The SRC argument supports standard [filename globbing](https://en.wikipedia.org/wiki/Glob_(programming)) rules. In other words, `*.csv` is a valid entry for the SRC argument. Festerize will ignore any files that do not end with `.csv`, so a command of `festerize *.*` should be safe to run. Festerize does not recursively search folders. 

The command must be run in the folder where the executable exists. Therefore, all SRC folders and files should be specified relative to the current folder.

## Example Cases
Version 2 is set and `ballin.csv` is processed
    
    ./festerize -v 2 ballin.csv

Version 2 is set, the new output is a folder called `testOutput`, and all `.csv` files in the folder un-festerized are processed
    
    ./festerize test/test-resources/un-festerized/*.csv -v 2 --output testOutput

Version 3 is set, only manifest metadata is updated, and all `.csv` files in the folder un-festerized are processed

    ./festerize -v 3 -m true test/test-resources/un-festerized/*.csv

