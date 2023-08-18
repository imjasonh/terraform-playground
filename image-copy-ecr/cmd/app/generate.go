/*
Copyright 2023 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

var timeNow = time.Now

const (
	audHeader = `Chainguard-Audience`
	idHeader  = `Chainguard-Identity`
)

// generateToken creates token using the supplied AWS credentials that can prove the user's AWS identity. Audience and identity are
// the Chainguard STS url (e.g https://issuer.enforce.dev) and the UID of the Chainguard assumable identity to assume via STS.
func generateToken(ctx context.Context, creds aws.Credentials, audience, identity string) (string, error) {
	req, err := http.NewRequest("POST", "https://sts.amazonaws.com", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new HTTP request: %w", err)
	}

	req.URL.Path = "/"
	req.URL.RawQuery = "Action=GetCallerIdentity&Version=2011-06-15"
	req.Header.Add("Accept", "application/json")
	req.Header.Add(audHeader, audience)
	req.Header.Add(idHeader, identity)

	const (
		// hashInit is the sha256 hash of an empty buffer, hex encoded.
		hashInit = `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`

		// STS service details for signing
		svc    = `sts`
		region = `us-east-1`
	)

	err = v4.NewSigner().SignHTTP(ctx, creds, req, hashInit, svc, region, timeNow())
	if err != nil {
		return "", fmt.Errorf("failed to sign GetCallerIdentity request with AWS credentials: %w", err)
	}

	var b bytes.Buffer
	err = req.Write(&b)
	if err != nil {
		return "", fmt.Errorf("failed to serialize GetCallerIdentity HTTP request to buffer: %w", err)
	}

	return base64.URLEncoding.EncodeToString(b.Bytes()), nil
}
