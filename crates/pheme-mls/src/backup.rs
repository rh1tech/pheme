//! Encrypted key backup.
//!
//! The device's exported MLS state (identity + every group's ratchet state) is the
//! only thing standing between the user and permanent loss of their chat history.
//! On web it lives in IndexedDB, which iOS Safari evicts after ~7 days of non-use;
//! a new device has none of it at all.
//!
//! This module seals that state under a key derived from a recovery passphrase, so
//! the ciphertext can be stored server-side and recovered later. The server never
//! sees the passphrase or the derived key — only the opaque blob. Losing the
//! passphrase loses the history; that is the deliberate trade for zero-knowledge
//! recovery.
//!
//! Key derivation is Argon2id (memory-hard, to slow guessing of a human
//! passphrase); the seal is AES-256-GCM (authenticated, so a wrong passphrase
//! fails cleanly rather than yielding garbage).

use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use argon2::{Algorithm, Argon2, Params, Version};
use zeroize::Zeroizing;

const SALT_LEN: usize = 16;
const NONCE_LEN: usize = 12;
const KEY_LEN: usize = 32;

// Argon2id cost. Deliberately well above OWASP's 19 MiB / t=2 floor: that floor is
// calibrated for a server-side login path that must stay fast under load, and this
// is the opposite situation. The sealed blob sits on a server we do not trust, so
// an attacker who takes the database can brute-force a human passphrase entirely
// offline, with no rate limit and unlimited attempts. Backup and restore are
// one-off, user-initiated operations — a slow derivation costs the honest user a
// moment once, and costs the attacker on every one of billions of guesses.
const ARGON_MEM_KIB: u32 = 64 * 1024; // 64 MiB
const ARGON_ITERS: u32 = 3;
const ARGON_LANES: u32 = 1;

/// A sealed backup: the random salt and nonce (public) plus the ciphertext. All
/// three are needed to recover, and none reveal anything without the passphrase.
pub struct Backup {
    pub salt: Vec<u8>,
    pub nonce: Vec<u8>,
    pub ciphertext: Vec<u8>,
}

// Zeroizing wipes the derived key from memory when it drops. WASM linear memory is
// never returned to the OS, so a key left behind lingers for the life of the tab and
// widens the blast radius of any unrelated memory-disclosure bug.
fn derive_key(passphrase: &[u8], salt: &[u8]) -> Result<Zeroizing<[u8; KEY_LEN]>, String> {
    let params = Params::new(ARGON_MEM_KIB, ARGON_ITERS, ARGON_LANES, Some(KEY_LEN))
        .map_err(|e| format!("argon2 params: {e}"))?;
    let argon = Argon2::new(Algorithm::Argon2id, Version::V0x13, params);
    let mut key = Zeroizing::new([0u8; KEY_LEN]);
    argon
        .hash_password_into(passphrase, salt, key.as_mut())
        .map_err(|e| format!("argon2 derive: {e}"))?;
    Ok(key)
}

/// Seals `plaintext` (the exported client state) under `passphrase`.
pub fn encrypt(passphrase: &[u8], plaintext: &[u8]) -> Result<Backup, String> {
    let mut salt = [0u8; SALT_LEN];
    getrandom::getrandom(&mut salt).map_err(|e| format!("rng salt: {e}"))?;
    let mut nonce = [0u8; NONCE_LEN];
    getrandom::getrandom(&mut nonce).map_err(|e| format!("rng nonce: {e}"))?;

    let key = derive_key(passphrase, &salt)?;
    let cipher = Aes256Gcm::new_from_slice(key.as_slice()).map_err(|e| format!("aes key: {e}"))?;
    let ciphertext = cipher
        .encrypt(Nonce::from_slice(&nonce), plaintext)
        .map_err(|e| format!("seal: {e}"))?;

    Ok(Backup {
        salt: salt.to_vec(),
        nonce: nonce.to_vec(),
        ciphertext,
    })
}

/// Recovers the plaintext state. A wrong passphrase or tampered blob fails the GCM
/// tag and returns an error, never partial/garbage state.
pub fn decrypt(
    passphrase: &[u8],
    salt: &[u8],
    nonce: &[u8],
    ciphertext: &[u8],
) -> Result<Vec<u8>, String> {
    let key = derive_key(passphrase, salt)?;
    let cipher = Aes256Gcm::new_from_slice(key.as_slice()).map_err(|e| format!("aes key: {e}"))?;
    cipher
        .decrypt(Nonce::from_slice(nonce), ciphertext)
        .map_err(|_| "wrong passphrase or corrupt backup".to_string())
}

#[cfg(all(test, not(target_arch = "wasm32")))]
mod tests {
    use super::*;

    #[test]
    fn round_trip_recovers_state() {
        let secret = b"exported client state bytes";
        let b = encrypt(b"correct horse battery staple", secret).unwrap();
        let out = decrypt(
            b"correct horse battery staple",
            &b.salt,
            &b.nonce,
            &b.ciphertext,
        )
        .unwrap();
        assert_eq!(out, secret);
    }

    #[test]
    fn wrong_passphrase_fails() {
        let b = encrypt(b"right passphrase", b"secret state").unwrap();
        assert!(decrypt(b"wrong passphrase", &b.salt, &b.nonce, &b.ciphertext).is_err());
    }

    #[test]
    fn ciphertext_hides_plaintext() {
        let b = encrypt(b"pw", b"the eagle lands at dawn").unwrap();
        assert!(!b.ciphertext.windows(5).any(|w| w == b"eagle"));
    }
}
