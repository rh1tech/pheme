// The Rust preview decrypt, as C. The extension is Swift and cannot use flutter_rust_bridge — that
// needs a Dart runtime, which an extension does not have and must not start. This is the plain C
// ABI written for exactly this caller: see mobile/rust/src/cabi.rs.
#include <stddef.h>
#include <stdint.h>

// Opens a sealed MLS state blob and decrypts ONE message for a preview. Returns a freshly
// allocated buffer and writes its length to out_len, or NULL when there is nothing to show —
// which is an ordinary outcome, not an error. The buffer must go back to pheme_preview_free with
// the same length.
uint8_t *pheme_preview_decrypt(const uint8_t *sealed_state, size_t sealed_state_len,
                               const uint8_t *data_key, size_t data_key_len,
                               const char *group_ids, const uint8_t *ciphertext,
                               size_t ciphertext_len, size_t *out_len);

// The attributed variant also returns the credential identity MLS authenticated as the sender.
// Both returned buffers must be freed independently with pheme_preview_free.
uint8_t *pheme_preview_decrypt_attributed(
    const uint8_t *sealed_state, size_t sealed_state_len,
    const uint8_t *data_key, size_t data_key_len,
    const char *group_ids, const uint8_t *ciphertext,
    size_t ciphertext_len, size_t *out_len,
    uint8_t **out_sender, size_t *out_sender_len);

void pheme_preview_free(uint8_t *ptr, size_t len);
