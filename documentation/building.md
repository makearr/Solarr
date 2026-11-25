# Building Solarr yourself

> **Level: 🔴 Expert.** Compile the binary or the image from source.

The toolchains run in Docker, so nothing needs installing but Docker, `git` and `make`.

```sh
# 1. Get the code (development happens on branch dev; main is the last release)
git clone https://github.com/makearr/Solarr.git
cd Solarr

# 2. Check everything: format, vet, lint, tests, generated code, licences
make verify

# 3. Build the web interface (lints and tests first); it is embedded into the binary
make web

# 4. Build the static binary (CGO_ENABLED=0) into ./solarr
make build

# 5. Or build a local Docker image, ghcr.io/makearr/solarr:dev
make image-local
```

Run the binary or the image:

```sh
./solarr serve --config ./config.yml

docker run -d --name solarr-dev -p 5454:5454 -v "$PWD/dev-config:/config" ghcr.io/makearr/solarr:dev
```

Other targets: `make test` (unit tests, `PKG=./internal/x/...` to narrow), `make race` (race detector),
`make bench BENCH_FILES=500000` (the 500k-file benchmarks), `make e2e` (a throwaway instance driven by Playwright),
`make api` (regenerate the Go server and TypeScript client from `api/openapi.yaml`), `make sqlc` (regenerate the
database code from `queries/`) and `make web-dev` (the Vite dev server on :5173, proxying the API). `make help` lists them all.

Without Docker you need **Go 1.27 or newer** (see `go.mod`) and **Node 22**. Versions are `YYYY.MM.R.BBBB`: year and
month of the release, the release number, and a build number that advances with every build ([versioning](versioning.md)).
The code is organised as described in the [codebase map](codebase-map.md).
