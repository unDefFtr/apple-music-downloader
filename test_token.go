package main

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
)

func GetToken() (string, error) {
	req, err := http.NewRequest("GET", "https://music.apple.com", nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	regex := regexp.MustCompile(`/assets/index~[^/]+\.js`)
	indexJsUri := regex.FindString(string(body))

	if indexJsUri == "" {
		return "", fmt.Errorf("index js not found in %s", string(body)[:100]) // print first 100 chars
	}

	fmt.Println("Found index JS URI:", indexJsUri)

	req, err = http.NewRequest("GET", "https://music.apple.com"+indexJsUri, nil)
	if err != nil {
		return "", err
	}

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Try a slightly more relaxed regex or print part of body if fail
	regex = regexp.MustCompile(`eyJh([^"]*)`)
	token := regex.FindString(string(body))

	if token == "" {
		return "", fmt.Errorf("token not found in JS file (len: %d)", len(body))
	}

	return token, nil
}

func main() {
	token, err := GetToken()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Token:", token)
}
