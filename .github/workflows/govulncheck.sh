govulncheck ./ddtrace/... | grep -Eo '\w+-\d+-\d+' | uniq >> results.txt
if [ $(cat  results.txt  | wc -l) -gt 0 ]; then
  echo "Found $(echo $RESULTS | wc -l )vulnerabilities: $RESULTS"
fi