# cmpcompress

## How it works

For each file in the given zip files, cmpcompress will:

1. Unpack the file.
2. Compress the file using different compression algorithms.
3. Compare the size of the compressed files.
4. Print the results as CSV.

## Example

```
# Compare different compression algorithms for each file in the given zip files.
cmpcompress file1.zip file2.zip ...

# Output:
src,file,algorithm,compression_ratio,speed_mb_per_sec,utility
my_archive.zip,file1.txt,gzip-1,5.23,150,784.50
my_archive.zip,file1.txt,gzip-6,8.11,120,973.20
my_archive.zip,file1.txt,gzip-9,9.50,80,760.00
my_archive.zip,file1.txt,kgzip-1,5.30,160,848.00
my_archive.zip,file1.txt,zstd-1,6.05,250,1512.50
my_archive.zip,file1.txt,zstd-2,9.80,200,1960.00
other_data.zip,image.png,gzip-6,1.10,50,55.00
other_data.zip,image.png,zstd-1,1.50,90,135.00
```
