#!/bin/bash

temp_file="tempfile.xml"

while read p; do
    if [[ "$p" =~ \<testcase.* ]]; then
        class=$(echo "$p" | grep -o '.v1/[^"]*"')
        file_name=$(echo "${class:3}" | sed 's/.$//') # trim off the edges to get the path
        new_line=$(echo "$p" | sed "s|<testcase|<testcase file=\"$file_name\"|")
        echo "$new_line" >> "$temp_file"
    else 
        echo "$p" >> "$temp_file"
    fi
done < ../../../gotestsum-report.xml

rm -f "$temp_file"

mv "$temp_file" ../../../gotestsum-report.xml