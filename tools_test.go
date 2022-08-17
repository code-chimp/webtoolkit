package webtoolkit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

type RoundTripFunc func(req *http.Request) *http.Response

func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

func MockTestClient(fn RoundTripFunc) *http.Client {
	return &http.Client{
		Transport: fn,
	}
}

func TestTools_RandomString(t *testing.T) {
	var testTools Tools
	const testLen = 10

	s := testTools.RandomString(testLen)

	if len(s) != testLen {
		t.Error("wrong length random string returned")
	}
}

var uploadTests = []struct {
	name          string
	allowedTypes  []string
	renameFile    bool
	errorExpected bool
}{
	{name: "allowed no rename", allowedTypes: []string{"image/jpeg", "image/png"}, renameFile: false, errorExpected: false},
	{name: "allowed rename", allowedTypes: []string{"image/jpeg", "image/png"}, renameFile: true, errorExpected: false},
	{name: "not allowed", allowedTypes: []string{"image/jpeg"}, renameFile: false, errorExpected: true},
}

func TestTools_UploadFiles(t *testing.T) {
	for _, entry := range uploadTests {
		pr, pw := io.Pipe()
		writer := multipart.NewWriter(pw)
		wg := sync.WaitGroup{}
		wg.Add(1)

		go func() {
			defer writer.Close()
			defer wg.Done()

			part, err := writer.CreateFormFile("file", "./testdata/cyborg-ape.png")
			if err != nil {
				t.Error(err)
			}

			f, err := os.Open("./testdata/cyborg-ape.png")
			if err != nil {
				t.Error("error opening image file", err)
			}
			defer f.Close()

			img, _, err := image.Decode(f)
			if err != nil {
				t.Error("error decoding image", err)
			}

			err = png.Encode(part, img)
			if err != nil {
				t.Error(err)
			}
		}()

		// read from the pipe which receives data
		request := httptest.NewRequest("POST", "/", pr)
		request.Header.Add("Content-Type", writer.FormDataContentType())

		var testTools Tools
		testTools.AllowedFileTypes = entry.allowedTypes

		files, err := testTools.UploadFiles(request, "./testdata/uploads/", entry.renameFile)
		if err != nil && !entry.errorExpected {
			t.Error(err)
		}

		if !entry.errorExpected {
			target := fmt.Sprintf("./testdata/uploads/%s", files[0].NewFileName)
			if _, err := os.Stat(target); os.IsNotExist(err) {
				t.Errorf("%s - expected file to exist: %s", entry.name, err.Error())
			}

			_ = os.Remove(target)
		}

		if !entry.errorExpected && err != nil {
			t.Errorf("%s: error expected but none received", entry.name)
		}

		wg.Wait()
	}
}

func TestTools_UploadOneFile(t *testing.T) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer writer.Close()

		part, err := writer.CreateFormFile("file", "./testdata/cyborg-ape.png")
		if err != nil {
			t.Error(err)
		}

		f, err := os.Open("./testdata/cyborg-ape.png")
		if err != nil {
			t.Error("error opening image file", err)
		}
		defer f.Close()

		img, _, err := image.Decode(f)
		if err != nil {
			t.Error("error decoding image", err)
		}

		err = png.Encode(part, img)
		if err != nil {
			t.Error(err)
		}
	}()

	// read from the pipe which receives data
	request := httptest.NewRequest("POST", "/", pr)
	request.Header.Add("Content-Type", writer.FormDataContentType())

	var testTools Tools

	file, err := testTools.UploadOneFile(request, "./testdata/uploads/", true)
	if err != nil {
		t.Error(err)
	}

	target := fmt.Sprintf("./testdata/uploads/%s", file.NewFileName)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Errorf("expected file to exist: %s", err.Error())
	}

	_ = os.Remove(target)
}

func TestTools_CreateDirIfNotExists(t *testing.T) {
	var testTools Tools
	target := "./dir1/dir2"

	err := testTools.CreateDirIfNotExists(target)
	if err != nil {
		t.Error(err)
	}

	err = testTools.CreateDirIfNotExists(target)
	if err != nil {
		t.Error(err)
	}

	_ = os.Remove(target)
}

var slugTests = []struct {
	name          string
	s             string
	expected      string
	errorExpected bool
}{
	{name: "valid string", s: "this is^.a__=TEST", expected: "this-is-a-test", errorExpected: false},
	{name: "empty string", s: "", expected: "", errorExpected: true},
	{name: "no roman characters", s: "-=+ _^%", expected: "", errorExpected: true},
	{name: "japanese string", s: "こんにちはテスト", expected: "", errorExpected: true},
	{name: "mixed japanese and roman", s: "hello,こんにちはテスト test", expected: "hello-test", errorExpected: false},
}

func TestTools_Slugify(t *testing.T) {
	var testTools Tools

	for _, entry := range slugTests {
		slug, err := testTools.Slugify(entry.s)

		if err != nil && !entry.errorExpected {
			t.Errorf("%s: unexpected error %s", entry.name, err.Error())
		}

		if !entry.errorExpected && slug != entry.expected {
			t.Errorf("%s: %s does not match expected output %s", entry.name, slug, entry.expected)
		}
	}
}

func TestTools_DownloadStaticFile(t *testing.T) {
	rr := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)

	var testTools Tools

	displayName := "loljohnny.jpg"
	testTools.DownloadStaticFile(rr, req, "./testdata", "tipfinger.jpg", displayName)

	res := rr.Result()
	defer res.Body.Close()

	expectedFileLength := "32152"
	actualFileLength := res.Header["Content-Length"][0]

	if actualFileLength != expectedFileLength {
		t.Errorf("Incorrect size: got %s, expected %s", actualFileLength, expectedFileLength)
	}

	expectedDisposition := fmt.Sprintf("attachment; filename=\"%s\"", displayName)
	actualDisposition := res.Header["Content-Disposition"][0]

	if actualDisposition != expectedDisposition {
		t.Errorf("Incorrect disposition: got %s, expected %s", actualDisposition, expectedDisposition)
	}

	_, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
}

var jsonReadTests = []struct {
	name               string
	json               string
	errorExpected      bool
	maxSize            int
	allowUnknownFields bool
}{
	{name: "good json", json: `{"foo": "bar"}`, errorExpected: false, maxSize: 512, allowUnknownFields: false},
	{name: "malformed JSON", json: `{"foo": "bar"`, errorExpected: true, maxSize: 512, allowUnknownFields: false},
	{name: "not JSON", json: "Yo bar to the foo", errorExpected: true, maxSize: 512, allowUnknownFields: false},
	{name: "invalid type", json: `{"foo": 1}`, errorExpected: true, maxSize: 512, allowUnknownFields: false},
	{name: "missing field name", json: `{baz: "bar"}`, errorExpected: true, maxSize: 512, allowUnknownFields: true},
	{name: "allowed unknown field", json: `{"foo": "bar", "baz": "fippity"}`, errorExpected: false, maxSize: 512, allowUnknownFields: true},
	{name: "disallowed unknown field", json: `{"foo": "bar", "baz": "fippity"}`, errorExpected: true, maxSize: 512, allowUnknownFields: false},
	{name: "payload too large", json: `{"foo": "bar"}`, errorExpected: true, maxSize: 5, allowUnknownFields: false},
	{name: "empty payload", json: "", errorExpected: true, maxSize: 512, allowUnknownFields: false},
	{name: "multiple payloads", json: `{"foo": "bar"}{"foo": "baz"}`, errorExpected: true, maxSize: 512, allowUnknownFields: false},
}

func TestTools_ReadJSON(t *testing.T) {
	var testTools Tools
	for _, entry := range jsonReadTests {
		testTools.MaxJSONSize = entry.maxSize
		testTools.AllowUnknownFields = entry.allowUnknownFields

		// declare variable to hold decoded JSON
		var decodedJSON struct {
			Foo string `json:"foo"`
		}

		// create an http request with the body from the table
		req, err := http.NewRequest("POST", "/", bytes.NewReader([]byte(entry.json)))
		if err != nil {
			t.Log("Error:", err)
		}

		// create a recorder
		rr := httptest.NewRecorder()

		// call sut
		err = testTools.ReadJSON(rr, req, &decodedJSON)

		if entry.errorExpected && err == nil {
			t.Errorf("%s: error expected, but none received", entry.name)
		}

		if !entry.errorExpected && err != nil {
			t.Errorf("%s: error not expected, but received - %s", entry.name, err.Error())
		}

		req.Body.Close()
	}
}

func TestTools_WriteJSON(t *testing.T) {
	var testTools Tools

	rr := httptest.NewRecorder()
	payload := JSONResponse{
		Error:   false,
		Message: "foo",
	}

	headers := make(http.Header)
	headers.Add("FOO", "BAR")

	err := testTools.WriteJSON(rr, http.StatusOK, payload, headers)
	if err != nil {
		t.Errorf("failed to write JSON: %v", err)
	}
}

func TestTools_ErrorJSON(t *testing.T) {
	var testTools Tools
	rr := httptest.NewRecorder()

	errorText := "this is a test error"
	errorStatus := http.StatusServiceUnavailable
	err := testTools.ErrorJSON(rr, errors.New(errorText), errorStatus)
	if err != nil {
		t.Error(err)
	}

	var payload JSONResponse
	decoder := json.NewDecoder(rr.Body)
	err = decoder.Decode(&payload)
	if err != nil {
		t.Error("error decoding JSON", err)
	}

	if !payload.Error {
		t.Error("error set to `false` but should be `true`")
	}

	if payload.Message != errorText {
		t.Errorf("error Message set to %s, expected %s", payload.Message, errorText)
	}

	if rr.Code != errorStatus {
		t.Errorf("request status set to %d, expected %d", rr.Code, errorStatus)
	}
}

func TestTools_PushJSONToRemote(t *testing.T) {
	client := MockTestClient(func(req *http.Request) *http.Response {
		// test request parameters
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString("ok")),
			Header:     make(http.Header),
		}
	})

	var data struct {
		Bar string `json:"bar"`
	}
	data.Bar = "baz"

	var testTool Tools

	_, _, err := testTool.PushJSONToRemote("http://example.net", data, client)
	if err != nil {
		t.Error("failed to call remote url:", err)
	}
}
