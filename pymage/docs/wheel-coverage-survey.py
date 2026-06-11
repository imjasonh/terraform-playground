#!/usr/bin/env python3
"""Survey what fraction of PyPI packages pymage can install (wheels only).

Measures wheel availability for linux/amd64 (and arm64) across:
  1. a random sample of all PyPI projects (unweighted long tail),
  2. a curated set of widely-depended-upon packages (popularity proxy),
  3. the resolved locks of real projects passed on the command line.

Usage:
    python3 wheel-coverage-survey.py [path/to/uv.lock ...]

Requires network access to PyPI. See wheel-coverage-survey.md for results.
"""
import concurrent.futures as cf
import json
import random
import sys
import tomllib
import urllib.request


def _get(url, accept=None, timeout=25):
    req = urllib.request.Request(url)
    if accept:
        req.add_header("Accept", accept)
    return urllib.request.urlopen(req, timeout=timeout)


def classify(name, version=None):
    """Classify the target release's distributions. None => no data."""
    try:
        url = f"https://pypi.org/pypi/{name}/json"
        if version:
            url = f"https://pypi.org/pypi/{name}/{version}/json"
        d = json.load(_get(url))
    except Exception:
        return None
    files = d.get("urls", [])
    if not files:
        return None
    pure = x86 = aarch = has = False
    for f in files:
        fn = f.get("filename", "")
        if not fn.endswith(".whl"):
            continue
        has = True
        if "-none-any.whl" in fn:
            pure = True
        if "x86_64" in fn and "linux" in fn:
            x86 = True
        if "aarch64" in fn and "linux" in fn:
            aarch = True
    return dict(has=has, pure=pure, x86=pure or x86, aarch=pure or aarch, wheelless=not has)


def survey(names, label):
    res = {}
    with cf.ThreadPoolExecutor(max_workers=24) as ex:
        futs = {ex.submit(classify, n): n for n in names}
        for f in cf.as_completed(futs):
            r = f.result()
            if r:
                res[futs[f]] = r
    n = len(res)
    if not n:
        print(f"{label}: no data")
        return
    pct = lambda k: sum(r[k] for r in res.values()) / n * 100
    print(f"\n=== {label} (n={n}) ===")
    print(f"  installable linux/amd64 : {pct('x86'):5.1f}%")
    print(f"  installable linux/arm64 : {pct('aarch'):5.1f}%")
    print(f"  pure-python (all targets): {pct('pure'):5.1f}%")
    print(f"  wheelless (sdist-only)  : {pct('wheelless'):5.1f}%")


TOP = """boto3 botocore urllib3 requests certifi charset-normalizer idna setuptools
six python-dateutil s3transfer pyyaml typing-extensions packaging numpy pandas
wheel cryptography rsa pyasn1 jmespath click colorama protobuf pip attrs scipy
markupsafe jinja2 grpcio pydantic pydantic-core fsspec pytz importlib-metadata
platformdirs pyparsing pluggy pytest tomli filelock virtualenv aiohttp yarl
multidict frozenlist cffi pycparser werkzeug flask sqlalchemy greenlet psutil
pillow beautifulsoup4 lxml httpx httpcore h11 anyio sniffio google-auth tqdm
regex tokenizers huggingface-hub safetensors torch transformers scikit-learn
matplotlib joblib networkx sympy orjson msgpack redis pymongo psycopg2-binary
asyncpg rich pygments typer uvicorn starlette fastapi gunicorn celery pyarrow
polars duckdb numba llvmlite zstandard watchfiles uvloop httptools websockets
python-multipart email-validator dnspython pynacl bcrypt paramiko ruff black
mypy coverage opencv-python onnxruntime grpcio-tools google-cloud-storage""".split()


def survey_lock(path):
    lf = tomllib.load(open(path, "rb"))
    pkgs = []
    for p in lf.get("package", []):
        src = p.get("source", {})
        if any(k in src for k in ("editable", "virtual", "directory", "workspace")):
            continue
        pkgs.append((p["name"], p.get("version")))
    blockers = []
    ok = 0
    with cf.ThreadPoolExecutor(max_workers=24) as ex:
        def chk(nv):
            r = classify(nv[0], nv[1]) or classify(nv[0])
            return nv[0], r
        for name, r in ex.map(chk, pkgs):
            if r and r["x86"]:
                ok += 1
            else:
                blockers.append(name)
    builds = "YES" if not blockers else "NO"
    print(f"\n  {path}: {ok}/{len(pkgs)} installable on linux/amd64 | builds: {builds}")
    if blockers:
        print(f"     blockers: {sorted(set(blockers))}")


def main():
    names = [p["name"] for p in json.load(
        _get("https://pypi.org/simple/", "application/vnd.pypi.simple.v1+json"))["projects"]]
    print("total PyPI projects:", len(names))
    random.seed(42)
    survey(random.sample(names, 900), "RANDOM sample of all PyPI (unweighted)")
    survey(sorted(set(TOP)), "CURATED popular packages (popularity proxy)")
    if len(sys.argv) > 1:
        print("\n=== REAL PROJECT LOCKS ===")
        for lock in sys.argv[1:]:
            survey_lock(lock)


if __name__ == "__main__":
    main()
