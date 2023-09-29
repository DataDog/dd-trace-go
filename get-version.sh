#!/bin/bash

# Define output file
output_file="supported-integration-versions.txt"
> $output_file

# Function to get the inputted integration's name
function process_integration {
    integration=$1
    
    # Run go list command to get the version
    version=$(go list -mod=mod -m -f '{{ .Version }}' "$integration")
    
    # Try to store values in output file
    if [[ -n "$version" ]]; then
        echo "$integration $version" >> "$output_file"
    else
        # Try to remove the version from the dependency name since no version was previously found
        dependency=${integration%/v*}
        dependency=${dependency%.v*}


        version=$(go list -mod=mod -m -f '{{ .Version }}' "$dependency")


        if [[ -n "$version" ]]; then
            # Save version to file along with dependency name
            echo "$dependency $version" >> "$output_file"
        else
            # Check for version of the directory name, ie: k8s.io/client-go/kubernetes" -> k8s.io/client-go as a final effort
            dependency=$(dirname "$integration")
            version=$(go list -mod=mod -m -f '{{ .Version }}' "$dependency")
            if [[ -n "$version" ]]; then
                # Save version to file along with dependency name
                echo "$dependency $version" >> "$output_file"
            else
                echo "FAILURE: No match found for $integration"
            fi
        fi
        echo
    fi
}

IFS=$'\n'

# Loop through subdirectories of "contrib"
for subdir in contrib/*; do
  # Check if subdir is a directory
  if [ -d "$subdir" ]; then
    # Parse file to get the integration name from tracer.MarkIntegrationImported function
    result=$(grep -r -Eo 'tracer\.MarkIntegrationImported\([^)]+\)' "$subdir")
    
    # loop through results as there may be multiple results for a subdir, ie: kafka has two modules in the same parent dir
    while IFS= read -r line; do      
        # Extract the variable name from the result
        variable=$(echo "$line" | sed -n -E 's/.*tracer\.MarkIntegrationImported\(([^)]+)\).*/\1/p')

        # Check if the variable has quotes on both sides
        if [[ $variable == "\""*"\"" ]]; then
            variable=$(echo $variable | sed 's/"//g')

            # parse the version
            process_integration $variable
        else
            # parse the variable and get the value from the file
            # Extract the file path from the result
            file=$(echo "$line" | cut -d ':' -f 1)

            # Find the value of the variable in the file
            value=$(grep -E "$variable\s*=" "$file" | awk -F'"' '{print $2}')

            # try to get the version if we found a value
            if [[ -n "$value" ]]; then
                # parse the version
                process_integration $value
            else
                echo "No match found for $file"
            fi
        fi
    done <<< "$result"
    unset result
  fi
done