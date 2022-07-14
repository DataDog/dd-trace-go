#include <stdint.h>
#include <stdio.h>
#include <strings.h>

#include <mach-o/dyld.h>
#include <mach-o/loader.h>
#include <mach-o/nlist.h>
#include <mach-o/stab.h>

// This finds C.malloc wrapper functions in a Mach-O executable.
//
// OVERVIEW:
//
// The Mach-O file format is used on macos for executables and dynamic
// libraries. We call a Mach-O file an object. Mach-O objects contain code and
// other data needed to run a program. An object starts with a header, followed
// by "load commands" which specify various kinds of data that a dynamic linker
// & loader will need to actually run the program or make the functions and data
// in a library available to other programs and libraries.
//
// We're looking for the following load commands:
//
//	* The symbol table, which specifies functions and data contained
//	  in the object.
//	* The string table, which contains the names of things referenced in
//	  other parts of the object.
//	* The *dynamic* symbol table, which points to subsets of the full
//	  symbol table, including to symbols which are externally defined and
//	  thus visible to other objects.
//
// We want to find these tables, and search them for the C.malloc wrapper
// functions. We then find their addresses and sizes and record the instruction
// address ranges so we can check them before profiling malloc.

extern int replaced_with_safety_wrapper;

static void find_mallocs(void);
__attribute__((constructor)) static void init(void) {
	uint32_t images = _dyld_image_count();
	if (images > 0) {
		find_mallocs();
	}
}

static void find_mallocs(void) {
	// The first image is the executable
	const struct mach_header_64 *m = (struct mach_header_64 *) _dyld_get_image_header(0);
	char *cursor = (char *) m;
	// skip the header, get to the load commands
	cursor += sizeof(struct mach_header_64);

	char *strings = NULL;
	struct symtab_command *tabcmd = NULL;
	struct nlist_64 *symtab = NULL;
	struct dysymtab_command *dytab = NULL;

	// linkedit_base is the base "virtual" memory address of the __LINKEDIT
	// segment which holds the string and symbol tables. Those tables have
	// offsets relative to this address.
	uint8_t *linkedit_base = NULL;
	// slide is the difference between where the data is actually loaded into
	// memory and the "virtual" memory address of the data. Addresses within
	// the object are relative to the virtual address, and thus need to be
	// adjusted by the slide so we can access them.
	intptr_t slide = _dyld_get_image_vmaddr_slide(0);

	for (uint32_t i = 0; i < m->ncmds; i++) {
		struct load_command *cmd = (struct load_command *) cursor;
		if (cmd->cmd == LC_SEGMENT_64) {
			struct segment_command_64 *seg = (struct segment_command_64 *) cursor;
			if (strcmp(seg->segname, SEG_LINKEDIT) == 0) {
				linkedit_base = (uint8_t *)(seg->vmaddr - seg->fileoff);
			}
		}
		if (cmd->cmd == LC_SYMTAB) {
			tabcmd = (struct symtab_command *) cursor;
		}
		if (cmd->cmd == LC_DYSYMTAB) {
			dytab = (struct dysymtab_command *) cursor;
		}
		cursor += cmd->cmdsize;
	}

	if ((tabcmd == NULL) || (dytab == NULL)) {
		return;
	}
	strings = (char *)(linkedit_base + slide + tabcmd->stroff);
	symtab = (struct nlist_64 *)(linkedit_base + slide + tabcmd->symoff);

	uintptr_t safety_wrapper = 0;
	// The __cgo_<...>_Cmalloc functions are external symbols
	for (uint32_t i = 0; i < dytab->nextdefsym; i++) {
		struct nlist_64 *sym = &symtab[i + dytab->iextdefsym];
		if ((sym->n_type & N_TYPE) == N_SECT) {
			// N_SECT means the symbol is actually defined in a
			// section of this object
			char *name = strings + sym->n_un.n_strx;
			if (strstr(name, "_Cfunc_safety_malloc_wrapper")) {
				safety_wrapper = (uintptr_t) sym->n_value;
			}
		}
	}

	for (uint32_t i = 0; i < dytab->nlocalsym; i++) {
		struct nlist_64 *sym = &symtab[i + dytab->ilocalsym];
		char *name = strings + sym->n_un.n_strx;
		if (strstr(name, "_Cfunc__Cmalloc")) {
			// TODO: Adjust these pointers??
			uintptr_t *p = (uintptr_t *)sym->n_value;
			*p = safety_wrapper;
		}
	}

	if (safety_wrapper != 0) {
		replaced_with_safety_wrapper = 1;
	}
}