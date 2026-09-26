// leveldb.h — curated FFI subset of upstream include/leveldb/c.h (v1.23).
// Single source of truth for this example. Regenerate the contract after editing:
//   tlua bindgen --header leveldb.h --out leveldb_raw.d.tl --shim leveldb_raw.tl --lib LevelDBRaw
//
// ABI notes (every signature below is ABI-identical to upstream; only these
// spellings differ, all pointer-for-pointer so the machine-level call is the
// same; verified against /home/tang/leveldb/include/leveldb/c.h):
//   - LEVELDB_EXPORT omitted (visibility attribute, not part of the call).
//   - Every parameter is named (upstream omits several names); names do not
//     affect ABI and keep bindgen output readable.
//   - `char** errptr` is spelled `void* errptr`: bindgen maps `char**` to
//     `string`, which would be wrong here (out-param, must stay cdata).
//   - `char* leveldb_get(...)` is spelled `void*`: the buffer is caller-owned
//     (must leveldb_free); as `string` LuaJIT would copy it and leak the
//     original pointer.
//   - `const char* leveldb_iter_key/value(...)` are spelled `void*`: borrowed
//     buffers with explicit lengths (binary-safe via ffi.string(p, len);
//     `string` conversion would truncate at the first NUL).
#ifndef TLEXAMPLE_LEVELDB_H
#define TLEXAMPLE_LEVELDB_H

#include <stddef.h>
#include <stdint.h>

typedef struct leveldb_t leveldb_t;
typedef struct leveldb_options_t leveldb_options_t;
typedef struct leveldb_readoptions_t leveldb_readoptions_t;
typedef struct leveldb_writeoptions_t leveldb_writeoptions_t;
typedef struct leveldb_iterator_t leveldb_iterator_t;

leveldb_options_t* leveldb_options_create(void);
void leveldb_options_destroy(leveldb_options_t* opt);
void leveldb_options_set_create_if_missing(leveldb_options_t* opt, uint8_t v);

leveldb_t* leveldb_open(const leveldb_options_t* opt, const char* name, void* errptr);
void leveldb_close(leveldb_t* db);

void leveldb_put(leveldb_t* db, const leveldb_writeoptions_t* opt,
                 const char* key, size_t keylen, const char* val,
                 size_t vallen, void* errptr);
void* leveldb_get(leveldb_t* db, const leveldb_readoptions_t* opt,
                  const char* key, size_t keylen, size_t* vallen,
                  void* errptr);
void leveldb_delete(leveldb_t* db, const leveldb_writeoptions_t* opt,
                    const char* key, size_t keylen, void* errptr);

leveldb_writeoptions_t* leveldb_writeoptions_create(void);
void leveldb_writeoptions_destroy(leveldb_writeoptions_t* opt);
leveldb_readoptions_t* leveldb_readoptions_create(void);
void leveldb_readoptions_destroy(leveldb_readoptions_t* opt);

void leveldb_free(void* ptr);

leveldb_iterator_t* leveldb_create_iterator(leveldb_t* db,
                                            const leveldb_readoptions_t* opt);
void leveldb_iter_destroy(leveldb_iterator_t* it);
uint8_t leveldb_iter_valid(const leveldb_iterator_t* it);
void leveldb_iter_seek_to_first(leveldb_iterator_t* it);
void leveldb_iter_next(leveldb_iterator_t* it);
void* leveldb_iter_key(const leveldb_iterator_t* it, size_t* klen);
void* leveldb_iter_value(const leveldb_iterator_t* it, size_t* vlen);
void leveldb_iter_get_error(const leveldb_iterator_t* it, void* errptr);

void leveldb_destroy_db(const leveldb_options_t* opt, const char* name,
                        void* errptr);

int leveldb_major_version(void);

#endif
