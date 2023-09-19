#!/bin/bash

# Define output file
output_file="output.txt"

# Function to get the inputted integration's name
function process_integration {
    integration=$1
    
    # Run go list command to get the version
    version=$(go list -mod=mod -m -f '{{ .Version }}' "$integration")
    
    # Try to store values in output file
    if [[ -n "$version" ]]; then
        echo "$integration $version" >> "$output_file"
    else
        echo "Trying to parse version for $integration"

        # Try to remove the version from the dependency name since no version was previously found
        dependency=${integration%/v*}
        dependency=${dependency%.v*}
        echo "dependency: $dependency"
        version=$(go list -mod=mod -m -f '{{ .Version }}' "$dependency")


        if [[ -n "$version" ]]; then
            # Save version to file along with integration name
            echo "$integration $version" >> "$output_file"
        else
            echo "FAILURE: No match found for $integration"
        fi
        echo
    fi
}

# Loop through subdirectories of "contrib"
for subdir in contrib/*; do
  # Check if subdir is a directory
  if [ -d "$subdir" ]; then
    # Parse file to get the integration name from tracer.MarkIntegrationImported function
    result=$(grep -r -Eo 'tracer\.MarkIntegrationImported\("[^"]+"\)' "$subdir" | cut -d'"' -f2)

    # Check if result is none
    if [ -z "$result" ]; then

        # Run the alternative grep command since we are probably using a variable and not a string
        result=$(grep -r -Eo 'tracer\.MarkIntegrationImported\([^)]+\)' "$subdir")
        
        # Iterate over each result since we may have multiple for contribs with multiple children
        while IFS= read -r line; do
            # Extract the file path from the result
            file=$(echo "$line" | cut -d ':' -f 1)
            
            # Extract the variable name from the result
            variable=$(echo "$line" | sed -n -E 's/.*tracer\.MarkIntegrationImported\(([^)]+)\).*/\1/p')
            
            # Find the value of the variable in the file
            value=$(grep -E "$variable\s*=" "$file" | awk -F'"' '{print $2}')

            # try to get the version if we found a value
            if [[ -n "$value" ]]; then
                process_integration $value
            else
                echo "No match found for $file"
            fi
        done <<< "$result"

        unset result
    fi

    IFS=$'\n'
    # loop through results as there may be multiple results for a subdir, ie: kafka has two modules in the same parent dir
    for integration in $result; do
        process_integration $integration
    done
  fi
done