#define _GNU_SOURCE
#include <elf.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

//#define _GNU_SOURCE (why doesn't this work?)
#include <link.h>
//#undef _GNU_SOURCE

extern int replaced_with_safety_wrapper;

// safe_readlink is a wrapper around readlink which will read the link
// in a size-independent way
static char *safe_readlink(const char *path) {
	size_t size = 256;
	ssize_t nread = 0;
	char *buf = NULL;
	do {
		size *= 2;
		free(buf); // OK if buf is NULL
		buf = calloc(1, size);
		if (buf == NULL) {
			return NULL;
		}
		ssize_t nread = readlink(path, buf, size);
		if (nread < 0) {
			free(buf);
			return NULL;
		}
	} while (nread == size);
	// if nread == size, we *might* have read to the end of the symlink or
	// there might be more. Make the buffer big enough to hold the value
	// plus room for the terminating 0 byte
	return buf;
}

static int callback(struct dl_phdr_info *info, size_t size, void *data) {
	uintptr_t *base_addr = data;
	*base_addr = info->dlpi_addr;
	return 1; // stop after the first call (covers the executable)
}

__attribute__ ((constructor)) static void init(void) {
	// To be technically correct we should check the base address where the
	// program was actually loaded, though I've only seen it be at base
	// address 0 on Linux.
	uintptr_t base_addr = 0;
	dl_iterate_phdr(callback, &base_addr);

	char *filename = safe_readlink("/proc/self/exe");
	if (filename == NULL) {
		return;
	}
	int fd = open(filename, O_RDONLY);
	free(filename);
	if (fd < 0) {
		return;
	}
	struct stat sb;
	if (fstat(fd, &sb) == -1) {
		close(fd);
		return;
	}

	void *file = mmap(
		NULL,        // loaded address hint, NULL means kernel chooses
		sb.st_size,  // size of data
		PROT_READ,   // access protections, we only want to read
		MAP_PRIVATE, // MAP_PRIVATE means copy-on-write mapping
		fd,          // fd to map
		0            // offset into fd
	);
	if (file == NULL) {
		return;
	}

	char *cursor = file;
	ElfW(Ehdr) *header = (ElfW(Ehdr) *) cursor;

	ElfW(Shdr) *section_headers = (ElfW(Shdr) *)(cursor + header->e_shoff);

	ElfW(Sym) *symtab_base = NULL;
	char *strtab_base = NULL;
	size_t nsyms = 0;

	for (uint16_t i = 0; i < header->e_shnum; i++) {
		ElfW(Shdr) *section = &section_headers[i];
		if (section->sh_type == SHT_SYMTAB) {
			symtab_base = (ElfW(Sym) *)(cursor + section->sh_offset);
			nsyms = section->sh_size / (sizeof(ElfW(Sym)));

			// The string table section corresponding to the symbol
			// table is linked via the sh_link field of the symbol
			// table section header.
			ElfW(Shdr) *string_section = &section_headers[section->sh_link];
			strtab_base = (char *)(cursor + string_section->sh_offset);
		}
	}

	if ((symtab_base == NULL) || (strtab_base == NULL)) {
		goto cleanup;
	}

	uintptr_t safety_wrapper = 0;
	for (size_t i = 0; i < nsyms; i++) {
		ElfW(Sym) *symbol = &symtab_base[i];
		if (ELF64_ST_TYPE(symbol->st_info) != STT_FUNC) {
			continue;
		}
		char *name = strtab_base + symbol->st_name;
		if (strstr(name, "_Cfunc_safety_malloc_wrapper")) {
			safety_wrapper = base_addr + symbol->st_value;
		}
	}
	if (safety_wrapper == 0) {
		goto cleanup;
	}

	for (size_t i = 0; i < nsyms; i++) {
		ElfW(Sym) *symbol = &symtab_base[i];
		if (ELF64_ST_BIND(symbol->st_info) != STB_LOCAL) {
			continue;
		}
		if (ELF64_ST_TYPE(symbol->st_info) != STT_OBJECT) {
			continue;
		}
		char *name = strtab_base + symbol->st_name;
		if (strstr(name, "_Cfunc__Cmalloc") != NULL) {
			uintptr_t *addr = (uintptr_t *)(base_addr + symbol->st_value);
			memcpy(addr, &safety_wrapper, sizeof(void *));
		}
	}

	replaced_with_safety_wrapper = 1;

cleanup:
	munmap(file, sb.st_size);
}
