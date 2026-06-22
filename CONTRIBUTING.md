# Contributing guidelines

Thanks for your interest! This is an independent, community-maintained project
(not part of the kubernetes / kubernetes-sigs orgs). Contributions are welcome.

## Developer Certificate of Origin (DCO)

This project uses the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a CLA. Sign off every commit to certify you wrote the patch (or have
the right to submit it under the project's Apache-2.0 license):

```sh
git commit -s -m "your message"   # adds a Signed-off-by: Your Name <email> trailer
```

## Contributing a patch

1. Open an issue describing the change you'd like to make.
2. Fork the repo, create a branch, develop and test your change.
   - `make test` (unit + envtest) and `gofmt`/`go vet` must pass; CI enforces them.
   - `make test-clusterctl-smoke` exercises the clusterctl install path on kind.
3. Submit a pull request with DCO sign-off. The maintainer ([OWNERS](OWNERS))
   will review.

See [README](README.md) and [docs/](docs/) for build, layout, and clusterctl usage.
