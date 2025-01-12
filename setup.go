// Copyright (C) 2025 by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type SetupSibling struct {
	Rfilename string `json:"rfilename"`
}

type SetupDatasetInfo struct {
	Siblings []SetupSibling `json:"siblings"`
}

func setupDownloadFile(url string, outputPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func setupGunzipFile(url string, outputPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, gzReader)
	return err
}

func setupFetchDatasetInfo(url string) (*SetupDatasetInfo, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var datasetInfo SetupDatasetInfo
	err = json.NewDecoder(resp.Body).Decode(&datasetInfo)
	if err != nil {
		return nil, err
	}

	return &datasetInfo, nil
}

func setupFilterAndDisplayFiles(siblings []SetupSibling) []SetupSibling {
	var dbFiles []SetupSibling
	for _, sibling := range siblings {
		if strings.HasSuffix(sibling.Rfilename, ".db.gz") {
			dbFiles = append(dbFiles, sibling)
			fmt.Printf("%d. %s\n", len(dbFiles), sibling.Rfilename)
		}
	}
	return dbFiles
}

func setupGetGGUFFileName(dbFile string) string {
	baseName := strings.TrimSuffix(dbFile, ".db.gz")
	parts := strings.Split(baseName, ".")
	if len(parts) == 2 {
		return parts[1] + ".gguf"
	}
	return ""
}

func setupFileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func Setup() {
	url := "https://huggingface.co/api/datasets/eja/wikilite"

	datasetInfo, err := setupFetchDatasetInfo(url)
	if err != nil {
		fmt.Println("Error fetching dataset info:", err)
		return
	}

	dbFiles := setupFilterAndDisplayFiles(datasetInfo.Siblings)
	if len(dbFiles) == 0 {
		fmt.Println("No .db.gz files found.")
		return
	}

	fmt.Print("Choose a file by number: ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	var choice int
	_, err = fmt.Sscanf(input, "%d", &choice)
	if err != nil || choice < 1 || choice > len(dbFiles) {
		fmt.Println("Invalid choice.")
		return
	}

	selectedDB := dbFiles[choice-1].Rfilename
	baseURL := "https://huggingface.co/datasets/eja/wikilite/resolve/main/"

	if setupFileExists("wikilite.db") {
		fmt.Println("A wikilite.db already exists in the current directory.")
		return
	}

	fmt.Println("Downloading and extracting", selectedDB)
	err = setupGunzipFile(baseURL+selectedDB, "wikilite.db")
	if err != nil {
		fmt.Println("Error downloading and extracting file:", err)
		return
	}
	fmt.Println("Saved as wikilite.db")

	ggufFile := setupGetGGUFFileName(selectedDB)
	if ggufFile != "" {
		if setupFileExists(ggufFile) {
			fmt.Printf("%s already exists in the current directory.\n", ggufFile)
			return
		}

		fmt.Println("Downloading corresponding gguf model:", ggufFile)
		err = setupDownloadFile(baseURL+ggufFile, ggufFile)
		if err != nil {
			fmt.Println("Error downloading .gguf file:", err)
			return
		}
		fmt.Println("Saved as", ggufFile)
	}
}
