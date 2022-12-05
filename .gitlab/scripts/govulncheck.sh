#!/usr/bin/env bash

go install golang.org/x/vuln/cmd/govulncheck@latest

function check_results {
  results=$(govulncheck $path | grep -Eo '\w+-\d+-\d+' | uniq | tr '\n' ' ')
  if [ $(echo $results | wc -l) -gt 0 ]; then
    echo "Found these vulnerabilities in $path:  $results"
    echo "Found these vulnerabilities in $path:  $results" >> full_results.txt
  fi
}
path=./ddtrace/... check_results
path=./appsec/... check_results
path=./internal/... check_results
path=./contrib/... check_results
path=./profiler/... check_results

echo full_results.txt | /usr/local/bin/pr-commenter --for-repo="$CI_PROJECT_NAME" --for-pr="$CI_COMMIT_REF_NAME" --header="Vulnerability report"



