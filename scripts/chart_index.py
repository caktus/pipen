"""Render the human-readable chart index page for the gh-pages branch.

Reads every Chart.yaml under the repo's charts/ directory and prints a
static HTML page listing the charts, their versions, and links to the
GitHub release and the packaged .tgz. The publish-chart workflow commits
the output as index.html on the gh-pages branch.
"""

import html
import os
import re

REPO = "https://github.com/caktus/pipen"
SITE = "https://caktus.github.io/pipen"

TEMPLATE = """<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>pipen charts</title>
<style>
body {{ font-family: -apple-system, "Segoe UI", Helvetica, Arial, sans-serif;
       margin: 2rem auto; max-width: 46rem; padding: 0 1rem; color: #1f2328; }}
table {{ border-collapse: collapse; width: 100%; }}
th, td {{ border-bottom: 1px solid #d0d7de; padding: .5rem .75rem;
         text-align: left; }}
pre {{ background: #f6f8fa; border-radius: 6px; padding: 1rem;
      overflow-x: auto; }}
</style>
</head>
<body>
<h1>pipen charts</h1>
<p>Helm chart repository, published from
<a href="{repo}">{org}/pipen</a>.</p>
<pre>helm repo add pipen {site}
helm repo update</pre>
<table>
<tr><th>Chart</th><th>Version</th><th>Description</th><th></th></tr>
{body}
</table>
<p>The machine-readable index is <a href="index.yaml">index.yaml</a>.</p>
</body>
</html>
"""


def read_chart(path):
    """Read name, version, and description from a Chart.yaml file."""
    meta = {}
    with open(path) as fh:
        for line in fh:
            match = re.match(r"^(name|version|description): (.+)$", line.rstrip())
            if match:
                meta[match.group(1)] = match.group(2).strip().strip('"')
    return meta


def row(meta):
    name = html.escape(meta.get("name", ""))
    version = html.escape(meta.get("version", ""))
    description = html.escape(meta.get("description", ""))
    release = "{repo}/releases/tag/{name}-{version}".format(
        repo=REPO, name=name, version=version
    )
    package = "{name}-{version}.tgz".format(name=name, version=version)
    return (
        "<tr><td><a href=\"{package}\">{name}</a></td><td>{version}</td>"
        "<td>{description}</td><td><a href=\"{release}\">release</a>"
        "</td></tr>".format(package=package, name=name, version=version,
                            description=description, release=release)
    )


def main():
    charts_dir = os.path.join(os.environ.get("GITHUB_WORKSPACE", "."), "charts")
    rows = []
    for entry in sorted(os.listdir(charts_dir)):
        chart = os.path.join(charts_dir, entry, "Chart.yaml")
        if os.path.isfile(chart):
            rows.append(row(read_chart(chart)))
    print(TEMPLATE.format(repo=REPO, org="caktus", site=SITE,
                          body="\n".join(rows)))


if __name__ == "__main__":
    main()
