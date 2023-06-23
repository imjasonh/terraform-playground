# `image-workflow`

This sets up a Cloud Run app to listen for `registry.push` events to a private Chainguard Registry group, and triggers a GitHub Actions workflow with that image ref as an input.

The Terraform does everything:

- builds the mirroring app into an image using `ko_build`
- deploys the app to a Cloud Run service

`TODO: package as a module like enforce-events`

Once things have been provisioned, this module outputs a `secret-command`
containing the command to run to upload your Github "personal access token" to
the Google Secret Manager secret the application will use, looking something
like this:

```shell
echo -n YOUR GITHUB PAT | \
  gcloud --project ... secrets versions add ... --data-file=-
```

The personal access token needs `actions:write` to trigger workflows.
