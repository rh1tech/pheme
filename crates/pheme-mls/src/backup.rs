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

#[cfg(all(test, not(target_arch = "wasm32")))]
mod integrity_tests {
    use super::*;

    /// What a backup is FOR: the transcript and key state of somebody's whole account, sitting on a
    /// server that must not be able to read it and must not be able to alter it undetectably.
    /// AES-GCM gives both, but only if every input is authenticated — so each of these flips one
    /// byte of one input and insists the open fails.

    #[test]
    fn a_tampered_ciphertext_is_refused() {
        let backup = encrypt(b"passphrase", b"the state").expect("seal");
        let mut corrupt = backup.ciphertext.clone();
        corrupt[0] ^= 0x01;

        let opened = decrypt(b"passphrase", &backup.salt, &backup.nonce, &corrupt);
        assert!(
            opened.is_err(),
            "a single flipped bit in the ciphertext was accepted; the server could alter a backup \
             without anyone noticing"
        );
    }

    #[test]
    fn a_tampered_tag_is_refused() {
        let backup = encrypt(b"passphrase", b"the state").expect("seal");
        let mut corrupt = backup.ciphertext.clone();
        let last = corrupt.len() - 1;
        corrupt[last] ^= 0x01; // the GCM tag lives at the end

        assert!(
            decrypt(b"passphrase", &backup.salt, &backup.nonce, &corrupt).is_err(),
            "a flipped bit in the authentication tag was accepted"
        );
    }

    #[test]
    fn a_swapped_nonce_is_refused() {
        let backup = encrypt(b"passphrase", b"the state").expect("seal");
        let other = encrypt(b"passphrase", b"the state").expect("seal");

        assert!(
            decrypt(b"passphrase", &backup.salt, &other.nonce, &backup.ciphertext).is_err(),
            "a backup opened under another backup's nonce"
        );
    }

    #[test]
    fn a_swapped_salt_is_refused() {
        let backup = encrypt(b"passphrase", b"the state").expect("seal");
        let other = encrypt(b"passphrase", b"the state").expect("seal");

        // A different salt derives a different key from the same passphrase, so this must fail even
        // though the passphrase is correct.
        assert!(
            decrypt(b"passphrase", &other.salt, &backup.nonce, &backup.ciphertext).is_err(),
            "a backup opened under another backup's salt"
        );
    }

    #[test]
    fn a_truncated_blob_is_refused() {
        let backup = encrypt(b"passphrase", b"the state").expect("seal");
        let truncated = &backup.ciphertext[..backup.ciphertext.len() - 1];

        assert!(
            decrypt(b"passphrase", &backup.salt, &backup.nonce, truncated).is_err(),
            "a truncated backup was accepted"
        );
    }

    #[test]
    fn an_empty_blob_is_refused() {
        let backup = encrypt(b"passphrase", b"the state").expect("seal");
        assert!(decrypt(b"passphrase", &backup.salt, &backup.nonce, &[]).is_err());
    }

    /// Sealing the same thing twice must not produce the same bytes. If it did, the server could
    /// tell that two backups have identical contents — that a user's state had not changed between
    /// them, or that two users hold the same transcript.
    #[test]
    fn sealing_twice_never_repeats_itself() {
        let first = encrypt(b"passphrase", b"the state").expect("seal");
        let second = encrypt(b"passphrase", b"the state").expect("seal");

        assert_ne!(first.salt, second.salt, "the salt repeated across two seals");
        assert_ne!(first.nonce, second.nonce, "the nonce repeated across two seals");
        assert_ne!(
            first.ciphertext, second.ciphertext,
            "sealing identical plaintext twice produced identical ciphertext"
        );
    }

    /// Both open correctly, which is what makes the randomness above safe rather than merely noisy.
    #[test]
    fn two_seals_of_the_same_state_both_open() {
        let first = encrypt(b"passphrase", b"the state").expect("seal");
        let second = encrypt(b"passphrase", b"the state").expect("seal");

        assert_eq!(
            decrypt(b"passphrase", &first.salt, &first.nonce, &first.ciphertext).unwrap(),
            b"the state"
        );
        assert_eq!(
            decrypt(b"passphrase", &second.salt, &second.nonce, &second.ciphertext).unwrap(),
            b"the state"
        );
    }

    /// An empty passphrase is a real input — a client with a bug could pass one — and it must
    /// behave like any other: it seals, and it opens under itself and nothing else.
    #[test]
    fn an_empty_passphrase_still_seals_and_opens_only_under_itself() {
        let backup = encrypt(b"", b"the state").expect("seal");

        assert_eq!(
            decrypt(b"", &backup.salt, &backup.nonce, &backup.ciphertext).unwrap(),
            b"the state"
        );
        assert!(
            decrypt(b"x", &backup.salt, &backup.nonce, &backup.ciphertext).is_err(),
            "a backup sealed under an empty passphrase opened under a different one"
        );
    }

    /// An account with no history yet still gets a backup, and it must round-trip rather than
    /// erroring — a new device restoring from it should find an empty transcript, not a failure.
    #[test]
    fn an_empty_state_round_trips() {
        let backup = encrypt(b"passphrase", b"").expect("seal");
        assert_eq!(
            decrypt(b"passphrase", &backup.salt, &backup.nonce, &backup.ciphertext).unwrap(),
            b""
        );
    }

    /// A real backup is a whole transcript, not a token. This is closer to the size that ships.
    #[test]
    fn a_large_state_round_trips() {
        let state: Vec<u8> = (0..256 * 1024).map(|i| (i % 251) as u8).collect();
        let backup = encrypt(b"passphrase", &state).expect("seal");

        assert_eq!(
            decrypt(b"passphrase", &backup.salt, &backup.nonce, &backup.ciphertext).unwrap(),
            state
        );
    }

    /// A passphrase is bytes, not ASCII: the recovery code normaliser upper-cases and strips, but
    /// nothing stops a caller passing UTF-8, and a byte-for-byte match must still be required.
    #[test]
    fn a_passphrase_is_compared_byte_for_byte() {
        let backup = encrypt("пароль".as_bytes(), b"the state").expect("seal");

        assert_eq!(
            decrypt("пароль".as_bytes(), &backup.salt, &backup.nonce, &backup.ciphertext).unwrap(),
            b"the state"
        );
        // One byte different in the middle of a multi-byte character.
        assert!(
            decrypt("паролъ".as_bytes(), &backup.salt, &backup.nonce, &backup.ciphertext).is_err()
        );
    }

    /// The salt is what makes two users with the SAME passphrase produce different keys. Without
    /// it, one cracked passphrase would open every backup that shared it.
    #[test]
    fn the_same_passphrase_yields_different_keys_under_different_salts() {
        let a = derive_key(b"passphrase", &[1u8; SALT_LEN]).expect("derive");
        let b = derive_key(b"passphrase", &[2u8; SALT_LEN]).expect("derive");
        assert_ne!(a.as_slice(), b.as_slice());
    }

    /// ...and the same passphrase with the same salt must derive the same key, or nothing could
    /// ever be reopened.
    #[test]
    fn key_derivation_is_deterministic() {
        let a = derive_key(b"passphrase", &[7u8; SALT_LEN]).expect("derive");
        let b = derive_key(b"passphrase", &[7u8; SALT_LEN]).expect("derive");
        assert_eq!(a.as_slice(), b.as_slice());
    }
}
