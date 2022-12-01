RESULTS=$(govulncheck ./ddtrace/... | grep -Eo '\w+-\d+-\d+' | uniq)
n=$(echo $RESULTS  | wc -l)
if [ $n -gt 0 ]; then
  echo "Found $n vulnerabilities: $RESULTS"
fi