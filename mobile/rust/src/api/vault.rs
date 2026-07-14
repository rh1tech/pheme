//! The at-rest seal for the persisted MLS state blob.
//!
//! This is NOT `pheme_mls::backup`. That one derives its key from a passphrase with Argon2id at
//! 64 MiB — correct for a recovery blob a human types a password into, and far too slow for
//! something rewritten on every single message. Here the key is already a random 32 bytes held in
//! the platform keystore; there is nothing to stretch, so this is a plain AES-256-GCM seal.
//!
//! Why seal it at all, when the file already sits in the app's private container: on Android the
//! container is readable on a rooted device, and on iOS a backup can carry the file off the phone.
//! The key stays in the Keychain/Keystore, which neither of those reaches.

use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use anyhow::{anyhow, Result};

const NONCE_LEN: usize = 12;
const KEY_LEN: usize = 32;

/// Cryptographically random bytes — the data key, and nothing else here needs them.
pub fn random_bytes(length: usize) -> Result<Vec<u8>> {
    let mut out = vec![0u8; length];
    getrandom::getrandom(&mut out).map_err(|e| anyhow!("random: {e}"))?;
    Ok(out)
}

/// Seals a blob. The nonce is 12 random bytes and is prepended to the ciphertext, so the caller stores
/// one opaque thing and never has to keep the two in step.
///
/// `domain` says WHAT is being sealed, and is bound in as additional authenticated data. It is not
/// decoration: two different things are sealed under this one key — the MLS key store, and the cache of
/// decrypted message bodies — and without a domain they would be cryptographically interchangeable.
/// Anyone able to write into the app's private container could swap a body cache in where the key store
/// belongs and it would open cleanly, handing arbitrary attacker-chosen bytes to `Client::import_state`.
/// With the domain bound in, a blob sealed for one purpose simply fails to open as the other.
pub fn vault_seal(domain: String, key: Vec<u8>, plaintext: Vec<u8>) -> Result<Vec<u8>> {
    let cipher = cipher(&key)?;
    let nonce = random_bytes(NONCE_LEN)?;

    let sealed = cipher
        .encrypt(
            Nonce::from_slice(&nonce),
            Payload {
                msg: &plaintext,
                aad: domain.as_bytes(),
            },
        )
        .map_err(|_| anyhow!("vault: seal failed"))?;

    let mut out = nonce;
    out.extend_from_slice(&sealed);
    Ok(out)
}

/// Opens a sealed blob. Fails on a wrong key, a truncated file, or a blob sealed for a DIFFERENT
/// domain — and the caller must treat all three as "not there", not as a crash: a corrupted or
/// half-written state is not something a retry fixes.
pub fn vault_open(domain: String, key: Vec<u8>, sealed: Vec<u8>) -> Result<Vec<u8>> {
    if sealed.len() <= NONCE_LEN {
        return Err(anyhow!("vault: sealed blob is truncated"));
    }
    let (nonce, ciphertext) = sealed.split_at(NONCE_LEN);

    cipher(&key)?
        .decrypt(
            Nonce::from_slice(nonce),
            Payload {
                msg: ciphertext,
                aad: domain.as_bytes(),
            },
        )
        .map_err(|_| anyhow!("vault: wrong key, wrong domain, or corrupt blob"))
}

fn cipher(key: &[u8]) -> Result<Aes256Gcm> {
    if key.len() != KEY_LEN {
        return Err(anyhow!("vault: key must be {KEY_LEN} bytes, got {}", key.len()));
    }
    Aes256Gcm::new_from_slice(key).map_err(|_| anyhow!("vault: bad key"))
}

#[cfg(test)]
mod tests {
    use super::*;

    const STATE: &str = "pheme.mls.state.v1";
    const BODIES: &str = "pheme.chat.bodies.v1";

    #[test]
    fn a_sealed_blob_opens_with_its_own_key() {
        let key = random_bytes(KEY_LEN).unwrap();
        let sealed = vault_seal(STATE.into(), key.clone(), b"state".to_vec()).unwrap();
        assert_eq!(vault_open(STATE.into(), key, sealed).unwrap(), b"state");
    }

    #[test]
    fn a_sealed_blob_does_not_open_with_another_key() {
        let sealed =
            vault_seal(STATE.into(), random_bytes(KEY_LEN).unwrap(), b"state".to_vec()).unwrap();
        assert!(vault_open(STATE.into(), random_bytes(KEY_LEN).unwrap(), sealed).is_err());
    }

    /// The key store and the body cache are sealed under the SAME key. Without a domain bound in, a
    /// body cache could be swapped in where the key store belongs and would open cleanly — handing
    /// attacker-chosen bytes straight to Client::import_state.
    #[test]
    fn a_blob_sealed_for_one_purpose_does_not_open_as_another() {
        let key = random_bytes(KEY_LEN).unwrap();
        let bodies = vault_seal(BODIES.into(), key.clone(), b"hello".to_vec()).unwrap();
        assert!(vault_open(STATE.into(), key, bodies).is_err());
    }

    #[test]
    fn the_nonce_is_never_reused() {
        let key = random_bytes(KEY_LEN).unwrap();
        let a = vault_seal(STATE.into(), key.clone(), b"state".to_vec()).unwrap();
        let b = vault_seal(STATE.into(), key, b"state".to_vec()).unwrap();
        assert_ne!(a[..NONCE_LEN], b[..NONCE_LEN]);
    }

    #[test]
    fn a_truncated_blob_is_an_error_not_a_panic() {
        assert!(vault_open(STATE.into(), random_bytes(KEY_LEN).unwrap(), vec![0u8; 4]).is_err());
    }
}
