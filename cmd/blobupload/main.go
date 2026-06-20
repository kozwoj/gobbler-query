// blobupload copies testdata into Azure Blob Storage so that BlobTableReader
// integration tests can run against real blobs.
//
// Credentials are supplied via environment variables (not a secrets file):
//
//	$env:GOBBLER_TEST_ACCOUNT = "gobblerstorage"
//	$env:GOBBLER_TEST_KEY     = "<key>"
//
// Run from the gobbler-query repo root:
//
//	go run ./cmd/blobupload [testdata-dir]
//
// testdata-dir defaults to "testdata". Each subdirectory becomes a container:
//
//	container "requests" ← testdata/requests/*.csv + requests.json
//	container "users"    ← testdata/users/*.csv    + users.json
//
// Containers are created if they do not already exist.
// Existing blobs with the same name are overwritten.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "blobupload: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	account := os.Getenv("GOBBLER_TEST_ACCOUNT")
	key := os.Getenv("GOBBLER_TEST_KEY")
	if account == "" || key == "" {
		return fmt.Errorf("GOBBLER_TEST_ACCOUNT and GOBBLER_TEST_KEY must be set")
	}

	testdataDir := "testdata"
	if len(os.Args) > 1 {
		testdataDir = os.Args[1]
	}

	cred, err := azblob.NewSharedKeyCredential(account, key)
	if err != nil {
		return fmt.Errorf("credential: %w", err)
	}
	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", account)
	client, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
	if err != nil {
		return fmt.Errorf("service client: %w", err)
	}
	fmt.Printf("Account: %s\n", account)

	// Each subdirectory of testdata maps to one container.
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", testdataDir, err)
	}

	ctx := context.Background()
	totalFiles := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		containerName := entry.Name()
		typeDir := filepath.Join(testdataDir, containerName)

		fmt.Printf("\nContainer %q:\n", containerName)
		cc := client.ServiceClient().NewContainerClient(containerName)
		_, err := cc.Create(ctx, nil)
		if err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("create container %s: %w", containerName, err)
		}
		fmt.Printf("  container ready\n")

		files, err := os.ReadDir(typeDir)
		if err != nil {
			return fmt.Errorf("read %s: %w", typeDir, err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			blobName := f.Name()
			localPath := filepath.Join(typeDir, blobName)
			if err := uploadFile(ctx, client, containerName, blobName, localPath); err != nil {
				return err
			}
			fmt.Printf("  uploaded %s\n", blobName)
			totalFiles++
		}
	}

	fmt.Printf("\nDone. %d file(s) uploaded.\n", totalFiles)
	return nil
}

func uploadFile(ctx context.Context, client *azblob.Client, containerName, blobName, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	_, err = client.UploadStream(ctx, containerName, blobName, f, &azblob.UploadStreamOptions{})
	if err != nil {
		return fmt.Errorf("upload %s/%s: %w", containerName, blobName, err)
	}
	return nil
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i <= len(s)-len("ContainerAlreadyExists"); i++ {
		if s[i:i+len("ContainerAlreadyExists")] == "ContainerAlreadyExists" {
			return true
		}
	}
	return false
}
