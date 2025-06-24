# vault.go

## Description
`vault.go` is a Go library designed to simplify interactions with HashiCorp Vault. It provides a set of utilities and functions to securely manage secrets, tokens, and configurations within your Go applications.

## Installation
To install `vault.go`, use `go get`:
```sh
go get github.com/mycarrier-devops/goLibMyCarrier/vault
```

## Reference
REF: https://github.com/hashicorp/vault-client-go 

## Usage
``` go
package main

import (
  vault "github.com/mycarrier-devops/goLibMyCarrier/vault"
)
func main() {
	ctx := context.Background()

  vaultClient, err := vault.VaultClient(ctx)
	if err != nil {
		log.Fatal("Error generating vault client: %s", err.Error())
	}

	secretData, err := vault.getKVSecret(ctx, vaultClient, "some/secret/path", "SomeMountPoint")
	if err != nil {
		log.Fatal("Error reading secret: %s", err.Error())
	}
	if secretData == nil {
		log.Fatal("Secret data is nil")
	}
	
  log.Printf("Secret read successfully. Secret1 = %s, Secret2 = %s", secretData["Secret1"], secretData["Secret2"])
}

```