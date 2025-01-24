#!/bin/bash

for file in "$@"; do
    temp_file="tempfile.xml"

    # force write a new line at the end of the gotestsum-report.xml, or else
    # the loop will skip the last line.
    # fixes issue with a missing </testsuites>
    echo -e "\n" >> $1

    while read p; do
        # we might try to report gotestsum-report.xml multiple times, so don't
        # calculate codeowners more times than we need
        if [[ "$p" =~ \<testcase && ! "$p" =~ "file=" ]]; then
            class=$(echo "$p" | grep -o '.v1/[^"]*"')
            file_name=$(echo "${class:3}" | sed 's/.$//') # trim off the edges to get the path
            new_line=$(echo "$p" | sed "s|<testcase|<testcase file=\"$file_name\"|")
            echo "$new_line" >> "$temp_file"
        else 
            echo "$p" >> "$temp_file"
        fi
    done < $file

    mv "$temp_file" $file
done 