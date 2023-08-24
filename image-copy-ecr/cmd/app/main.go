/*
Copyright 2023 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecrcreds "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/coreos/go-oidc"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/kelseyhightower/envconfig"
)

var amazonKeychain authn.Keychain = authn.NewKeychainFromHelper(ecrcreds.NewECRHelper(ecrcreds.WithLogger(log.Writer())))

var env = struct {
	Issuer      string `envconfig:"ISSUER_URL" required:"true"`
	Group       string `envconfig:"GROUP" required:"true"`
	Identity    string `envconfig:"IDENTITY" required:"true"`
	Region      string `envconfig:"REGION" required:"true"`
	DstRepo     string `envconfig:"DST_REPO" required:"true"`
	FullDstRepo string `envconfig:"FULL_DST_REPO" required:"true"`
}{}

func init() {
	if err := envconfig.Process("", &env); err != nil {
		log.Fatalf("failed to process env var: %s", err)
	}
}
func main() { lambda.Start(handler) }

func handler(ctx context.Context, levent events.LambdaFunctionURLRequest) (resp string, err error) {
	defer func() {
		if err != nil {
			log.Printf("=== GOT ERROR: %v", err)
			log.Printf("body: %+v", levent.Body)
			log.Printf("env: %+v", env)
		}
	}()

	// We expect Chainguard webhooks to pass an Authorization header.
	auth := strings.TrimPrefix(levent.Headers["authorization"], "Bearer ")
	if auth == "" {
		return "", fmt.Errorf("auth header missing")
	}

	// Construct a verifier that ensures tokens are issued by the Chainguard
	// issuer we expect and are intended for a customer webhook.
	provider, err := oidc.NewProvider(ctx, env.Issuer)
	if err != nil {
		return "", fmt.Errorf("failed to create provider: %v", err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: "customer"})
	if tok, err := verifier.Verify(ctx, auth); err != nil {
		return "", fmt.Errorf("unable to verify token: %w", err)
	} else if !strings.HasPrefix(tok.Subject, "webhook:") {
		return "", fmt.Errorf("subject should be from the Chainguard webhook component, got: %s", tok.Subject)
	} else if group := strings.TrimPrefix(tok.Subject, "webhook:"); group != env.Group {
		return "", fmt.Errorf("this token is intended for %s, wanted one for %s", group, env.Group)
	}

	if levent.Headers["ce-type"] != PushEventType {
		// This doesn't represent a push, so there's nothing to do.
		log.Printf("event type is %q, skipping", levent.Headers["ce-type"])
		return "", nil
	}

	data := Occurrence{}
	if err := json.Unmarshal([]byte(levent.Body), &data); err != nil {
		return "", fmt.Errorf("unable to unmarshal event: %w", err)
	}

	if data.Body.Error != nil {
		// This represents a push error, so there's nothing to do.
		log.Printf("event body has error, skipping: %+v", data.Body.Error)
		return "", nil
	}
	if data.Body.Tag == "" || data.Body.Type != "manifest" {
		// This doesn't represent a tag push, so there's nothing to sync.
		log.Printf("event body is not a tag push, skipping: %q %q", data.Body.Tag, data.Body.Type)
		return "", nil
	}

	// Attempt to create the repo; if it exists, ignore it.
	// ECR requires you to pre-create repos before pushing to them.
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load configuration, %w", err)
	}
	repo := filepath.Join(env.DstRepo, filepath.Base(data.Body.Repository))
	if _, err := ecr.New(ecr.Options{
		Region:      env.Region,
		Credentials: cfg.Credentials,
	}).CreateRepository(ctx, &ecr.CreateRepositoryInput{
		RepositoryName: &repo,
	}); err != nil {
		var rae *types.RepositoryAlreadyExistsException
		if errors.As(err, &rae) {
			log.Printf("ECR repo %s already exists", repo)
		} else {
			return "", fmt.Errorf("creating ECR repo %s: %w", repo, err)
		}
	} else {
		log.Printf("Created ECR repo %s", repo)
	}

	// Sync src:tag to dst:tag.
	src := "cgr.dev/" + data.Body.Repository + ":" + data.Body.Tag
	dst := filepath.Join(env.FullDstRepo, filepath.Base(data.Body.Repository)) + ":" + data.Body.Tag
	log.Printf("Copying %s to %s...", src, dst)
	if err := crane.Copy(src, dst,
		crane.WithAuthFromKeychain(authn.NewMultiKeychain(
			amazonKeychain,
			cgKeychain{env.Issuer, env.Region, env.Identity},
		))); err != nil {
		return "", fmt.Errorf("copying image: %w", err)
	}
	log.Printf("Copied %s to %s", src, dst)
	return "", nil
}

type cgKeychain struct {
	issuer, region, identity string
}

func (k cgKeychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	if res.RegistryStr() != "cgr.dev" {
		return authn.Anonymous, nil
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration, %w", err)
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve credentials, %w", err)
	}

	awsTok, err := generateToken(ctx, creds, k.region, k.issuer, k.identity)
	if err != nil {
		return nil, fmt.Errorf("generating AWS token: %w", err)
	}

	url := (&url.URL{
		Scheme: "https",
		Host:   strings.TrimPrefix(k.issuer, "https://"),
		Path:   "/sts/exchange",
		RawQuery: url.Values{
			"aud":      []string{res.RegistryStr()},
			"identity": []string{k.identity},
		}.Encode(),
	}).String()
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+awsTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	all, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got HTTP %d to /sts/exchange: %s", resp.StatusCode, all)
	}
	var m map[string]string
	if err := json.NewDecoder(bytes.NewReader(all)).Decode(&m); err != nil {
		return nil, err
	}
	return &authn.Basic{
		Username: "_token",
		Password: m["token"],
	}, nil
}
