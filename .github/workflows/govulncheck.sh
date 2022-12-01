found=0
function check_results {
  results=$(govulncheck $path | grep -Eo '\w+-\d+-\d+' | uniq)
  if [ $(echo $results | wc -l) -gt 0 ]; then
    echo "Found these vulnerabilities in $path: $results" >&2
    echo $results
    found=$(($found || 1))
  fi
}
path=./ddtrace/... check_results
path=./appsec/... check_results
path=./internal/... check_results
path=./contrib/... check_results
path=./profiler/... check_results
exit $found