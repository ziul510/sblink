# Copyright 2026 ziul510
# SPDX-License-Identifier: Apache-2.0

import base64
import csv
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path


NOTICES = "THIRD_PARTY_NOTICES"
CSV = "third_party/licenses.csv"
FILES = (NOTICES, CSV)
NOTICE_FILE = re.compile(r"^(?:licen[cs]e|unlicen[cs]e|copying|notice|patents|third.party.notices)(?:$|[._-])", re.I)
PRIMARY_LICENSE = re.compile(r"^(?:licen[cs]e|unlicen[cs]e|copying)(?:$|[._-])", re.I)
SPDX = re.compile(r"\(*[A-Za-z0-9][A-Za-z0-9.()+ /-]*")
MARKER = "<!-- sblink-dependency-licenses -->"


def run(*args, cwd=None, data=None):
    return subprocess.run(args, cwd=cwd, input=data, stdout=subprocess.PIPE, check=True).stdout


def json_stream(raw):
    decoder = json.JSONDecoder()
    rest = raw.decode("utf-8").lstrip()
    while rest:
        value, end = decoder.raw_decode(rest)
        yield value
        rest = rest[end:].lstrip()


def notice_files(directory):
    return sorted(p.name for p in directory.iterdir() if p.is_file() and NOTICE_FILE.match(p.name))


def read_notice(directory, name):
    file = (directory / name).resolve()
    if Path(name).is_absolute() or not file.is_relative_to(directory.resolve()) or not file.is_file():
        raise ValueError(f"Missing or invalid notice file {name} in {directory.name}")
    return file.read_text(encoding="utf-8-sig")


def classify(files, work):
    # Use an explicit SPDX expression when upstream publishes one in its license text
    declared = {
        match.strip()
        for file in files
        for match in re.findall(r"^\s*SPDX-License-Identifier:\s*(.+)$", file.read_text(encoding="utf-8-sig"), re.M)
    }
    if len(declared) == 1 and SPDX.fullmatch(next(iter(declared))):
        return next(iter(declared))
    report = work / "classification.json"
    run("identify_license", "-json", str(report), *files, cwd=work)
    results = json.loads(report.read_text(encoding="utf-8"))
    licenses = []
    for result in results:
        matches = {
            m["Name"] for m in result["Classifications"]
            if m["Name"] != "Copyright" and m["Confidence"] >= 0.90
        }
        licenses.append(matches)
    if not licenses or len(licenses) != len(files) or any(
        len(matches) != 1 or matches != licenses[0] for matches in licenses
    ):
        raise ValueError("License text could not be classified unambiguously")
    name = next(iter(licenses[0]))
    if not SPDX.fullmatch(name):
        raise ValueError("License classifier did not return an SPDX identifier")
    return name


@dataclass(frozen=True)
class Dependency:
    name: str
    version: str
    license: str
    texts: tuple[str, ...]

    @property
    def digest(self):
        return hashlib.sha256(json.dumps(self.texts, ensure_ascii=False).encode("utf-8")).hexdigest()

    @property
    def label(self):
        return f"`{self.name} {self.version}` (`{self.license}`)"


BEGIN = "=== BEGIN GO DEPENDENCIES ===\n"
END = "=== END GO DEPENDENCIES ==="
TEMPLATE = "SBLink third-party Go module notices\n\n" + BEGIN + END + "\n"


def scan(revision):
    with tempfile.TemporaryDirectory(prefix="sblink-license-") as tmp:
        work = Path(tmp)
        (work / "go.mod").write_bytes(git_file(revision, "go.mod"))
        checksum = git_file(revision, "go.sum", optional=True)
        if checksum is not None:
            (work / "go.sum").write_bytes(checksum)

        # Resolve Go manifests without compiling or running code from the pull request
        modules = list(json_stream(run("go", "list", "-m", "-mod=readonly", "-json", "all", cwd=work)))
        if any(module.get("Replace") for module in modules):
            raise ValueError("Go module replacements require manual license review")
        refs = [f'{module["Path"]}@{module["Version"]}' for module in modules if not module.get("Main")]
        if not refs:
            return {}

        deps = {}
        for module in json_stream(run("go", "mod", "download", "-json", *refs, cwd=work)):
            name, version, directory = module["Path"], module["Version"], Path(module["Dir"])
            files = notice_files(directory)
            primary = [directory / file for file in files if PRIMARY_LICENSE.match(file)]
            if not primary:
                raise ValueError(f"No Go license file for {name} {version}")
            try:
                license = classify(primary, work)
                texts = tuple(read_notice(directory, file) for file in files)
            except ValueError as error:
                raise ValueError(f"{name} {version}: {error}") from error
            deps[name] = Dependency(name, version, license, texts)
        if {f"{dep.name}@{dep.version}" for dep in deps.values()} != set(refs):
            raise ValueError("Go returned an incomplete module inventory")
        return deps


def warnings(before, after):
    changes = []
    for name in sorted(before.keys() | after.keys()):
        old, new = before.get(name), after.get(name)
        if old is None:
            changes.append(f"- Go dependency added: {new.label}.")
        elif new is None:
            changes.append(f"- Go dependency removed: {old.label}.")
        elif old.license != new.license:
            changes.append(f"- Go license changed from {old.label} to {new.label}.")
        elif old.texts != new.texts:
            changes.append(f"- Go license or notice text changed for {new.label}.")
    return changes


def render(notices, deps):
    if notices.count(BEGIN) != 1 or notices.count(END) != 1:
        raise ValueError("Missing or duplicate Go dependency notice markers")
    prefix, body = notices.split(BEGIN)
    _, suffix = body.split(END)
    report = io.StringIO()
    writer = csv.writer(report, lineterminator="\n")
    writer.writerow(("Ecosystem", "Dependency", "Version", "License", "Notice SHA256"))
    section = []
    for name in sorted(deps):
        dep = deps[name]
        writer.writerow(("Go", name, dep.version, dep.license, dep.digest))
        texts = "".join(text if text.endswith("\n") else text + "\n" for text in dep.texts)
        section.append(f"========== {name} {dep.version} ({dep.license}) ==========\n" + texts)
    if not section:
        section.append("No external Go modules.\n")
    return {
        NOTICES: (prefix + BEGIN + "\n".join(section) + END + suffix).encode("utf-8"),
        CSV: report.getvalue().encode("utf-8"),
    }


def git_file(revision, path, optional=False):
    if optional and not run("git", "ls-tree", "--name-only", revision, "--", path).strip():
        return None
    return run("git", "show", f"{revision}:{path}")


def snapshot(head, deps):
    notices = git_file(head, NOTICES, optional=True)
    return render(TEMPLATE if notices is None else notices.decode("utf-8"), deps)


def verify_baseline():
    deps = scan("HEAD")
    for path, content in snapshot("HEAD", deps).items():
        if git_file("HEAD", path, optional=True) != content:
            raise ValueError(f"{path} does not match SBLink's Go dependencies")
    print(f"Verified the license baseline for {len(deps)} external Go modules.")


def audit(head, output):
    base = run("git", "merge-base", "HEAD", head).decode().strip()
    before, after = scan(base), scan(head)
    changes = warnings(before, after)
    summary = "\n".join(changes) if changes else "No Go dependency license changes detected."
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a", encoding="utf-8") as file:
        file.write(f"## Dependency licenses\n\n{summary}\n")
    additions = [
        {"path": name, "contents": base64.b64encode(contents).decode("ascii")}
        for name, contents in snapshot(head, after).items()
        if contents != git_file(head, name, optional=True)
    ]
    output.mkdir(parents=True, exist_ok=True)
    (output / "result.json").write_text(json.dumps({"changes": changes, "additions": additions}), encoding="utf-8")


def api(endpoint, method="GET", data=None, *flags):
    args = ["gh", "api", endpoint, "--method", method, *flags]
    if data is not None:
        args += ["--input", "-"]
    return json.loads(run(*args, data=None if data is None else json.dumps(data).encode("utf-8")))


def eligible_pr(pr, repo):
    return (
        pr["state"] == "open"
        and pr["user"]["login"] == "dependabot[bot]"
        and pr["base"]["ref"] == "main"
        and pr["head"]["ref"].startswith("dependabot/")
        and (pr["head"]["repo"] or {}).get("full_name") == repo
    )


def target(number):
    repo = os.environ["GITHUB_REPOSITORY"]
    pr = api(f"repos/{repo}/pulls/{number}")
    if not eligible_pr(pr, repo):
        raise ValueError("Select an open Dependabot PR from this repository targeting main")
    return pr["head"]["sha"]


def comment(endpoint, number, head, report, warn):
    comments = f"{endpoint}/issues/{number}/comments"
    existing = next((
        entry for page in api(comments, "GET", None, "--paginate", "--slurp") for entry in page
        if entry["user"]["login"] == "github-actions[bot]" and entry["body"].startswith(MARKER)
    ), None)
    if not warn and not existing:
        return
    repo = os.environ["GITHUB_REPOSITORY"]
    run_url = f'{os.environ["GITHUB_SERVER_URL"]}/{repo}/actions/runs/{os.environ["GITHUB_RUN_ID"]}'
    body = {"body": f"{MARKER}\n{report}\n\n[License check for `{head[:12]}`]({run_url})"}
    if existing:
        api(f"{endpoint}/issues/comments/{existing['id']}", "PATCH", body)
    else:
        api(comments, "POST", body)


def commit_notices(repo, head, additions):
    if any(file["path"] not in FILES for file in additions):
        raise ValueError("Unexpected path in generated license update")
    commit = api("graphql", "POST", {
        "query": "mutation($input: CreateCommitOnBranchInput!) { createCommitOnBranch(input: $input) { commit { oid } } }",
        "variables": {"input": {
            "branch": {"repositoryNameWithOwner": repo, "branchName": head["ref"]},
            "expectedHeadOid": head["sha"],
            "message": {"headline": "chore: update dependency license notices [skip dependabot]"},
            "fileChanges": {"additions": additions},
        }},
    })
    print("Updated Dependabot branch at", commit["data"]["createCommitOnBranch"]["commit"]["oid"])


def publish(output, status):
    repo = os.environ["GITHUB_REPOSITORY"]
    number = int(os.environ["PR_NUMBER"])
    expected_head = os.environ["PR_HEAD"]
    if not expected_head:
        return
    endpoint = f"repos/{repo}"
    pr = api(f"{endpoint}/pulls/{number}")
    head = pr["head"]
    if not eligible_pr(pr, repo) or head["sha"] != expected_head:
        return

    if status != "success":
        comment(endpoint, number, head["sha"], "**License check failed.** Review the workflow run before merging.", True)
        return

    result = json.loads((output / "result.json").read_text(encoding="utf-8"))
    changes = result["changes"]
    report = ("**Review these dependency license changes before merging.**\n\n" + "\n".join(changes)
              if changes else "No Go dependency license changes detected.")
    try:
        comment(endpoint, number, head["sha"], report, bool(changes))
    finally:
        # A comment permission failure must not prevent a valid notice commit
        if result["additions"]:
            commit_notices(repo, head, result["additions"])


if __name__ == "__main__":
    if sys.argv[1] == "target":
        print(target(int(sys.argv[2])))
    elif sys.argv[1] == "verify-baseline":
        verify_baseline()
    elif sys.argv[1] == "audit":
        audit(sys.argv[2], Path(sys.argv[3]))
    elif sys.argv[1] == "publish":
        publish(Path(sys.argv[2]), sys.argv[3])
