#!/usr/bin/env python3
"""Crawl chainguard-actions repos and identify tags with actual hardening applied.

Each forked action repo mirrors upstream tags. Tags that include HARDENING.md describe
what the hardening agent did. Tags with zero findings were scanned but unchanged; tags
with one or more findings had security fixes applied in the fork.

Example (no hardening):
  https://github.com/chainguard-actions/docker-setup-buildx-action/blob/v4.0.0/HARDENING.md

Example (hardening applied):
  https://github.com/chainguard-actions/imjasonh-setup-crane/blob/v0.5/HARDENING.md
"""

from __future__ import annotations

import argparse
import csv
import json
import os
import re
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import asdict, dataclass, field
from typing import Any

DEFAULT_ORG = "chainguard-actions"
USER_AGENT = "chainguard-hardening-crawler/1.0"

SUMMARY_RE = re.compile(
    r"(?P<findings>\d+) finding\(s\) were identified and resolved across "
    r"(?P<iterations>\d+) iteration\(s\)\."
)
FINDING_HEADER_RE = re.compile(r"^### (.+?) \(severity: (.+?)\)\s*$", re.MULTILINE)


@dataclass
class HardeningReport:
    repo: str
    tag: str
    findings: int
    iterations: int
    url: str
    finding_types: list[str] = field(default_factory=list)
    severities: list[str] = field(default_factory=list)


class GitHubClient:
    def __init__(self, token: str | None = None) -> None:
        self.token = token
        self._remaining: int | None = None
        self._reset_at: float | None = None
        self._api_lock = threading.Lock()

    def _headers(self) -> dict[str, str]:
        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": USER_AGENT,
            "X-GitHub-Api-Version": "2022-11-28",
        }
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def _throttle(self) -> None:
        if self._remaining is not None and self._remaining < 5 and self._reset_at:
            sleep_for = max(0.0, self._reset_at - time.time()) + 1.0
            if sleep_for > 0:
                print(f"GitHub API rate limit low; sleeping {sleep_for:.0f}s", file=sys.stderr)
                time.sleep(sleep_for)

    def request_json(self, url: str) -> Any:
        with self._api_lock:
            self._throttle()
            req = urllib.request.Request(url, headers=self._headers())
            with urllib.request.urlopen(req, timeout=60) as resp:
                self._remaining = int(resp.headers.get("X-RateLimit-Remaining", "60"))
                reset = resp.headers.get("X-RateLimit-Reset")
                self._reset_at = float(reset) if reset else None
                return json.load(resp)

    def request_text(self, url: str) -> str | None:
        req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return resp.read().decode("utf-8", errors="replace")
        except urllib.error.HTTPError as exc:
            if exc.code == 404:
                return None
            raise

    def list_org_repos(self, org: str) -> list[dict[str, Any]]:
        repos: list[dict[str, Any]] = []
        page = 1
        while True:
            url = (
                f"https://api.github.com/orgs/{org}/repos"
                f"?per_page=100&page={page}&type=all"
            )
            batch = self.request_json(url)
            if not batch:
                break
            repos.extend(batch)
            if len(batch) < 100:
                break
            page += 1
        return repos

def list_tags_git(org: str, repo: str) -> list[str]:
    """List tags via git ls-remote to avoid per-repo GitHub API calls."""
    remote = f"https://github.com/{org}/{repo}.git"
    proc = subprocess.run(
        ["git", "ls-remote", "--tags", remote],
        capture_output=True,
        text=True,
        timeout=90,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"git ls-remote failed for {repo}")

    tags: list[str] = []
    for line in proc.stdout.splitlines():
        if "\t" not in line:
            continue
        ref = line.split("\t", 1)[1]
        if ref.endswith("^{}"):
            continue
        if ref.startswith("refs/tags/"):
            tags.append(ref.removeprefix("refs/tags/"))
    return tags


def parse_hardening_md(org: str, repo: str, tag: str, content: str) -> HardeningReport | None:
    match = SUMMARY_RE.search(content)
    if not match:
        return None

    finding_types: list[str] = []
    severities: list[str] = []
    for header_match in FINDING_HEADER_RE.finditer(content):
        finding_types.append(header_match.group(1))
        severities.append(header_match.group(2))

    findings = int(match.group("findings"))
    iterations = int(match.group("iterations"))
    return HardeningReport(
        repo=repo,
        tag=tag,
        findings=findings,
        iterations=iterations,
        url=f"https://github.com/{org}/{repo}/blob/{tag}/HARDENING.md",
        finding_types=finding_types,
        severities=severities,
    )


def inspect_tag(client: GitHubClient, org: str, repo: str, tag: str) -> HardeningReport | None:
    raw_url = f"https://raw.githubusercontent.com/{org}/{repo}/{tag}/HARDENING.md"
    content = client.request_text(raw_url)
    if content is None:
        return None
    return parse_hardening_md(org, repo, tag, content)


def inspect_repo(
    client: GitHubClient,
    org: str,
    repo: str,
    hardened_only: bool,
) -> list[HardeningReport]:
    reports: list[HardeningReport] = []
    try:
        tags = list_tags_git(org, repo)
    except (RuntimeError, subprocess.TimeoutExpired) as exc:
        print(f"warning: could not list tags for {repo}: {exc}", file=sys.stderr)
        return reports

    for tag in tags:
        report = inspect_tag(client, org, repo, tag)
        if report is None:
            continue
        if hardened_only and report.findings == 0:
            continue
        reports.append(report)
    return reports


def write_json(path: str, reports: list[HardeningReport], summary: dict[str, Any]) -> None:
    payload = {"summary": summary, "reports": [asdict(r) for r in reports]}
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2)
        fh.write("\n")


def write_csv(path: str, reports: list[HardeningReport]) -> None:
    with open(path, "w", encoding="utf-8", newline="") as fh:
        writer = csv.DictWriter(
            fh,
            fieldnames=[
                "repo",
                "tag",
                "findings",
                "iterations",
                "finding_types",
                "severities",
                "url",
            ],
        )
        writer.writeheader()
        for report in reports:
            writer.writerow(
                {
                    "repo": report.repo,
                    "tag": report.tag,
                    "findings": report.findings,
                    "iterations": report.iterations,
                    "finding_types": ";".join(report.finding_types),
                    "severities": ";".join(report.severities),
                    "url": report.url,
                }
            )


def print_table(reports: list[HardeningReport]) -> None:
    if not reports:
        print("No matching hardened reports found.")
        return

    rows = [
        (r.repo, r.tag, str(r.findings), str(r.iterations), ", ".join(r.finding_types) or "-")
        for r in sorted(reports, key=lambda item: (-item.findings, item.repo, item.tag))
    ]
    headers = ("REPO", "TAG", "FINDINGS", "ITERATIONS", "FINDING_TYPES")
    widths = [len(h) for h in headers]
    for row in rows:
        for idx, cell in enumerate(row):
            widths[idx] = max(widths[idx], len(cell))

    def fmt_row(cells: tuple[str, ...]) -> str:
        return "  ".join(cell.ljust(widths[idx]) for idx, cell in enumerate(cells))

    print(fmt_row(headers))
    print(fmt_row(tuple("-" * w for w in widths)))
    for row in rows:
        print(fmt_row(row))


def build_summary(
    org: str,
    repo_count: int,
    reports: list[HardeningReport],
    hardened_only: bool,
) -> dict[str, Any]:
    hardened = [r for r in reports if r.findings > 0]
    repos_with_hardening = sorted({r.repo for r in hardened})
    return {
        "org": org,
        "repos_scanned": repo_count,
        "tags_in_output": len(reports),
        "tags_with_hardening_applied": len(hardened),
        "repos_with_hardening_applied": len(repos_with_hardening),
        "output_filtered_to_hardened_only": hardened_only,
        "repos_with_hardening_applied_list": repos_with_hardening,
    }


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Identify chainguard-actions fork tags where hardening fixes were applied.",
    )
    parser.add_argument("--org", default=DEFAULT_ORG, help="GitHub org to crawl (default: chainguard-actions)")
    parser.add_argument(
        "--hardened-only",
        action="store_true",
        default=True,
        help="Only include tags with findings > 0 (default: true)",
    )
    parser.add_argument(
        "--include-unhardened",
        action="store_true",
        help="Include tags scanned with zero findings",
    )
    parser.add_argument("--repo", action="append", default=[], help="Limit crawl to specific repo name(s)")
    parser.add_argument("--limit", type=int, default=0, help="Limit number of repos (0 = all)")
    parser.add_argument("--workers", type=int, default=8, help="Concurrent repo workers (default: 8)")
    parser.add_argument("--output-json", help="Write full results to JSON file")
    parser.add_argument("--output-csv", help="Write results to CSV file")
    parser.add_argument("--quiet", action="store_true", help="Suppress progress logs")
    args = parser.parse_args()

    hardened_only = args.hardened_only and not args.include_unhardened
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    client = GitHubClient(token=token)

    if not args.quiet:
        auth_note = (
            "authenticated"
            if token
            else "unauthenticated (GITHUB_TOKEN optional; only used for org repo listing)"
        )
        print(f"Crawling org {args.org} ({auth_note})", file=sys.stderr)

    if args.repo:
        repos = [{"name": name} for name in args.repo]
    else:
        repos = client.list_org_repos(args.org)

    if args.limit > 0:
        repos = repos[: args.limit]

    if not args.quiet:
        print(f"Scanning {len(repos)} repos with {args.workers} workers", file=sys.stderr)

    all_reports: list[HardeningReport] = []
    with ThreadPoolExecutor(max_workers=max(1, args.workers)) as pool:
        futures = {
            pool.submit(inspect_repo, client, args.org, repo["name"], hardened_only): repo["name"]
            for repo in repos
        }
        completed = 0
        for future in as_completed(futures):
            repo_name = futures[future]
            completed += 1
            try:
                reports = future.result()
                all_reports.extend(reports)
            except Exception as exc:  # noqa: BLE001 - surface per-repo failures and continue
                print(f"warning: failed scanning {repo_name}: {exc}", file=sys.stderr)
            if not args.quiet and completed % 25 == 0:
                print(f"  progress: {completed}/{len(repos)} repos", file=sys.stderr)

    all_reports.sort(key=lambda item: (-item.findings, item.repo, item.tag))
    summary = build_summary(
        args.org,
        len(repos),
        all_reports,
        hardened_only=hardened_only,
    )

    if args.output_json:
        write_json(args.output_json, all_reports, summary)
    if args.output_csv:
        write_csv(args.output_csv, all_reports)

    if not args.quiet:
        print(
            f"\nFound {summary['tags_with_hardening_applied']} hardened tag(s) "
            f"across {summary['repos_with_hardening_applied']} repo(s).",
            file=sys.stderr,
        )

    print_table(all_reports)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
