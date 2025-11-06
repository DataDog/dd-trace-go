#!/bin/bash

RESULT_PATH="${1:-.}"
if [ ! -d "$RESULT_PATH" ]; then
    echo "Error: Result path does not exist"
    exit 1
fi

cd "$RESULT_PATH" || exit 1

shopt -s nullglob
files=(gotestsum-report*.xml)
shopt -u nullglob

echo "Found ${#files[@]} JUnit file(s) to process in '$RESULT_PATH'"

for file in "${files[@]}"; do
    echo "Processing: $file"
    temp_file="${file}_tmp.xml"

    # force write a new line at the end of the gotestsum-report.xml, or else
    # the loop will skip the last line.
    # fixes issue with a missing </testsuites>
    echo -e "\n" >> "$file"

    while IFS= read -r p || [ -n "$p" ]; do
        # we might try to report gotestsum-report.xml multiple times, so don't
        # calculate codeowners more times than we need
        if [[ "$p" =~ \<testcase && ! "$p" =~ "file=" ]]; then
            # in v2, some of our paths contain a "/v2" before the subdirectory path, but
            # the contribs do not. We optionally remove it.
            class=$(echo "$p" | sed "s|/v2||")
            class=$(echo "$class" | grep -o 'dd-trace-go/[^"]*"')
            file_name=$(echo "${class:11}" | sed 's/.$//') # trim off the edges to get the path
            new_line=$(echo "$p" | sed "s|<testcase \([^>]*\)>|<testcase \1 file=\"$file_name\">|")
            echo "$new_line" >> "$temp_file"
        else 
            echo "$p" >> "$temp_file"
        fi
    done < $file

    mv "$temp_file" $file
done 