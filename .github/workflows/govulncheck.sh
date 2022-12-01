function check_results {
  results=$(echo $content | grep -Eo '\w+-\d+-\d+' | uniq)
  if [ $(echo $results | wc -l) -gt 0 ]; then
  echo "Found these vulnerabilities in $path:"
  echo $results
  exit 1
  fi
}
content=$(govulncheck ./ddtrace/...) path=./ddtrace/... check_results