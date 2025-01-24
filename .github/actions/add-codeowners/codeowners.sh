#!/bin/bash

temp_file="tempfile.xml"

# force write a new line at the end of the gotestsum-report.xml, or else
# the loop will skip the last line.
# fixes issue with a missing </testsuites>
echo -e "\n" >> $1

while read p; do
    if [[ "$p" =~ \<testcase ]]; then
        class=$(echo "$p" | grep -o '.v1/[^"]*"')
        file_name=$(echo "${class:3}" | sed 's/.$//') # trim off the edges to get the path
        new_line=$(echo "$p" | sed "s|<testcase|<testcase file=\"$file_name\"|")
        echo "$new_line" >> "$temp_file"
    else 
        echo "$p" >> "$temp_file"
    fi
done < $1

mv "$temp_file" $1