// Package webtoolkit is a container for utility functions
package webtoolkit

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// randomStringSource supplies the characters used to generate random strings
const randomStringSource = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+"

// Tools is used to instantiate this module. Any variable will have access
// to all of the methods with the receiver *Tools
type Tools struct {
	MaxFileSize        int
	AllowedFileTypes   []string
	MaxJSONSize        int
	AllowUnknownFields bool
}

// RandomString returns a string of random characters of length n, using
// randomStringSource as the source for the string
func (t *Tools) RandomString(n int) string {
	randString, charSource := make([]rune, n), []rune(randomStringSource)

	for i := range randString {
		p, _ := rand.Prime(rand.Reader, len(charSource))
		x, y := p.Uint64(), uint64(len(charSource))
		randString[i] = charSource[x%y]
	}

	return string(randString)
}

// UploadedFile is used to save information about an uploaded file.
type UploadedFile struct {
	NewFileName      string
	OriginalFileName string
	FileSize         int64
}

// UploadOneFile is a convenience method that calls UploadFiles, but expects only one file.
// It returns an UploadedFile and potentially an error.
// If the optional last parameter is set to `false` we will not rename the file(s) but keep the original
// filename.
func (t *Tools) UploadOneFile(r *http.Request, uploadDir string, rename ...bool) (*UploadedFile, error) {
	renameFile := true
	if t.MaxFileSize == 0 {
		t.MaxFileSize = 1024 * 1024 * 1024
	}

	if len(rename) > 0 {
		renameFile = rename[0]
	}

	files, err := t.UploadFiles(r, uploadDir, renameFile)
	if err != nil {
		return nil, err
	}

	return files[0], nil
}

// UploadFiles uploads one or more files to a specified directory, and gives the file a random name.
// It returns a slice of UploadedFile and potentially an error.
// If the optional last parameter is set to `false` we will not rename the file(s) but keep the original
// filename.
func (t *Tools) UploadFiles(r *http.Request, uploadDir string, rename ...bool) ([]*UploadedFile, error) {
	renameFile := true
	if t.MaxFileSize == 0 {
		t.MaxFileSize = 1024 * 1024 * 1024
	}

	if len(rename) > 0 {
		renameFile = rename[0]
	}

	var uploadedFiles []*UploadedFile

	err := r.ParseMultipartForm(int64(t.MaxFileSize))
	if err != nil {
		return nil, errors.New("the uploaded file is too large")
	}

	err = t.CreateDirIfNotExists(uploadDir)
	if err != nil {
		return nil, errors.New("cannot create/utilize upload directory")
	}

	for _, fileHeaders := range r.MultipartForm.File {
		for _, fileHeader := range fileHeaders {
			uploadedFiles, err = func(uploadedFiles []*UploadedFile) ([]*UploadedFile, error) {
				var uploadedFile UploadedFile

				infile, err := fileHeader.Open()
				if err != nil {
					return nil, err
				}
				defer infile.Close()

				// check to see if the file type is permitted
				buff := make([]byte, 512)
				_, err = infile.Read(buff)
				if err != nil {
					return nil, err
				}

				allowed := false
				fileType := http.DetectContentType(buff)

				if len(t.AllowedFileTypes) > 0 {
					for _, x := range t.AllowedFileTypes {
						if strings.EqualFold(x, fileType) {
							allowed = true
							break
						}
					}
				} else {
					allowed = true
				}

				if !allowed {
					return nil, errors.New(fmt.Sprintf("files of type '%s' are not allowed", fileType))
				}

				// we're good, so rewind
				_, err = infile.Seek(0, 0)
				if err != nil {
					return nil, err
				}

				uploadedFile.OriginalFileName = fileHeader.Filename

				if renameFile {
					uploadedFile.NewFileName = fmt.Sprintf("%s%s", t.RandomString(25), filepath.Ext(fileHeader.Filename))
				} else {
					uploadedFile.NewFileName = fileHeader.Filename
				}

				var outfile *os.File
				defer outfile.Close()

				if outfile, err = os.Create(filepath.Join(uploadDir, uploadedFile.NewFileName)); err != nil {
					return nil, err
				} else {
					fileSize, err := io.Copy(outfile, infile)
					if err != nil {
						return nil, err
					}

					uploadedFile.FileSize = fileSize
				}

				uploadedFiles = append(uploadedFiles, &uploadedFile)

				return uploadedFiles, nil
			}(uploadedFiles)

			if err != nil {
				return uploadedFiles, err
			}
		}
	}

	return uploadedFiles, nil
}

// CreateDirIfNotExists creates a directory and all necessary parents if they do not exist
func (t *Tools) CreateDirIfNotExists(path string) error {
	const mode = 0755

	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, mode)
		if err != nil {
			return err
		}
	}

	return nil
}

// Slugify converts string s into a URL safe slug
func (t *Tools) Slugify(s string) (string, error) {
	if strings.Trim(s, " ") == "" {
		return "", errors.New("empty string not permitted")
	}

	var re = regexp.MustCompile(`[^a-z\d]+`)
	slug := strings.Trim(re.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(slug) == 0 {
		return "", errors.New("removing non-characters returns zero length slug")
	}

	return slug, nil
}

// DownloadStaticFile sends file to the client and attempts to force the browser to download the file,
// saving it as the value provided in the displayName parameter
func (t *Tools) DownloadStaticFile(w http.ResponseWriter, r *http.Request, pathName, displayName string) {
	if _, err := os.Stat(pathName); os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusNotFound)
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", displayName))

	http.ServeFile(w, r, pathName)
}

// JSONResponse is used to relay JSON payloads
type JSONResponse struct {
	Error   bool        `json:"error"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ReadJSON attempts to convert the body of a request from JSON into a go data variable
func (t *Tools) ReadJSON(w http.ResponseWriter, r *http.Request, data interface{}) error {
	// try to prevent malicious content size
	maxBytes := 1024 * 1024
	if t.MaxJSONSize != 0 {
		maxBytes = t.MaxJSONSize
	}

	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))

	dec := json.NewDecoder(r.Body)
	if !t.AllowUnknownFields {
		dec.DisallowUnknownFields()
	}

	err := dec.Decode(data)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		unknownFieldErr := "json: unknown field"

		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("body contains badly formed JSON at character %d", syntaxError.Offset)

		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)
			}
			return fmt.Errorf("body contains incorrect JSON at character %d", unmarshalTypeError.Offset)

		case errors.As(err, &invalidUnmarshalError):
			return fmt.Errorf("error unmarshalling JSON: %s", err.Error())

		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly formed JSON (unexpected EOF)")

		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")

		case strings.HasPrefix(err.Error(), unknownFieldErr):
			fieldname := strings.TrimPrefix(err.Error(), unknownFieldErr)
			return fmt.Errorf("body contains unknown key %s", fieldname)

		case err.Error() == "http: request body too large":
			return fmt.Errorf("body must not be larger than %d bytes", maxBytes)

		default:
			return err
		}
	}

	err = dec.Decode(&struct{}{})
	if err != io.EOF {
		return errors.New("body must not contain more than one JSON payload")
	}

	return nil
}

// WriteJSON takes a response status and arbitrary data and writes JSON to the client
func (t *Tools) WriteJSON(w http.ResponseWriter, status int, data interface{}, headers ...http.Header) error {
	out, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if len(headers) > 0 {
		for key, value := range headers[0] {
			w.Header()[key] = value
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_, err = w.Write(out)
	if err != nil {
		return err
	}

	return nil
}

// ErrorJSON takes an error and optionally a status code, and sends a formatted JSON error
func (t *Tools) ErrorJSON(w http.ResponseWriter, err error, status ...int) error {
	statusCode := http.StatusBadRequest
	if len(status) > 0 {
		statusCode = status[0]
	}

	var payload JSONResponse
	payload.Error = true
	payload.Message = err.Error()

	return t.WriteJSON(w, statusCode, payload)
}

// PushJSONToRemote posts arbitrary JSON data to the specified uri and returns the response, status code, and error.
// The standard http.Client is used unless an optional one is supplied in the optional client parameter.
func (t *Tools) PushJSONToRemote(uri string, data interface{}, client ...*http.Client) (*http.Response, int, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	httpClient := &http.Client{}
	if len(client) > 0 {
		httpClient = client[0]
	}

	req, err := http.NewRequest("POST", uri, bytes.NewBuffer(payload))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	defer res.Body.Close()

	return res, res.StatusCode, nil
}
