//! A plain C ABI, for the iOS NotificationServiceExtension.
//!
//! Everything else in this crate is reached through flutter_rust_bridge, which assumes a Dart
//! runtime. An extension has no Dart runtime and no Flutter engine — it is a small Swift process
//! that iOS spawns for a few hundred milliseconds — so it links the same static library directly
//! and calls this instead. Same crate, same MLS, third binding: `api/` for Dart, `wasm.rs` in
//! pheme-mls for the browser, and this for Swift.
//!
//! ------------------------------------------------------------------------------------------
//! IT NEVER WRITES. NOT TO THE KEY STORE, NOT TO ANYTHING.
//!
//! The extension is a SECOND PROCESS holding the same key store as the app, which is exactly what
//! the single-client rule exists to prevent — two ratchets advancing independently and racing to
//! disk leaves one saved over the other, and that is every message after it permanently
//! unreadable.
//!
//! So this takes the sealed state by value, opens it in memory, reads one message, and drops the
//! lot. It is built on `PreviewClient`, which has no `export_state`, so there is nowhere for the
//! advanced ratchet to go even if a later edit wanted to put it somewhere. The app's own copy is
//! untouched and still holds an unconsumed key for that message, and decrypts it again for real
//! when the user opens the chat.
//! ------------------------------------------------------------------------------------------
//!
//! The surface is deliberately ONE function. Every extra entry point is another chance for the
//! Swift side to hold Rust state across a call, and holding state is the thing that must not
//! happen here.

use std::os::raw::c_char;
use std::ptr;
use std::slice;

use pheme_mls::PreviewClient;

use crate::api::vault::vault_open;

/// Must match MlsStore's `_domain` in mobile/lib/src/crypto/mls_store.dart. The seal binds it as
/// additional data, so a blob sealed for the body cache simply fails to open as the key store —
/// getting this wrong is a decrypt failure, not a silent mix-up.
const STATE_DOMAIN: &str = "pheme.mls.state.v1";

/// Opens a sealed MLS state blob and decrypts one message for a notification preview.
///
/// Returns a freshly allocated buffer holding the plaintext, and writes its length to `out_len`.
/// Returns null when there is nothing to show — which is an ordinary outcome here, not an error:
/// a wrong key, a message for a group this device does not hold, control traffic, a state blob
/// from a newer build. The caller shows the server's generic text in every one of those cases.
///
/// The returned buffer must be handed back to [`pheme_preview_free`] with the same length.
///
/// # Safety
///
/// Every pointer must either be null or point to at least the stated number of readable bytes.
/// `group_ids` is a NUL-terminated UTF-8 string of newline-separated group ids; a conversation can
/// have more than one, because a retired group's messages still decrypt under its old id.
#[no_mangle]
pub unsafe extern "C" fn pheme_preview_decrypt(
    sealed_state: *const u8,
    sealed_state_len: usize,
    data_key: *const u8,
    data_key_len: usize,
    group_ids: *const c_char,
    ciphertext: *const u8,
    ciphertext_len: usize,
    out_len: *mut usize,
) -> *mut u8 {
    if out_len.is_null() {
        return ptr::null_mut();
    }
    *out_len = 0;

    let Some(sealed) = as_slice(sealed_state, sealed_state_len) else {
        return ptr::null_mut();
    };
    let Some(key) = as_slice(data_key, data_key_len) else {
        return ptr::null_mut();
    };
    let Some(ct) = as_slice(ciphertext, ciphertext_len) else {
        return ptr::null_mut();
    };
    if group_ids.is_null() {
        return ptr::null_mut();
    }
    let Ok(groups) = std::ffi::CStr::from_ptr(group_ids).to_str() else {
        return ptr::null_mut();
    };

    // Nothing below may panic across the FFI boundary — unwinding into Swift is undefined
    // behaviour, and this runs in a process whose only job is to draw a notification.
    let result = std::panic::catch_unwind(|| {
        let state = vault_open(STATE_DOMAIN.to_string(), key.to_vec(), sealed.to_vec()).ok()?;
        let client = PreviewClient::import_state(&state).ok()?;
        for id in groups.split('\n').filter(|g| !g.is_empty()) {
            let gid = id.as_bytes();
            if !client.has_group(gid) {
                continue;
            }
            if let Ok(Some(plaintext)) = client.decrypt(gid, ct) {
                return Some(plaintext);
            }
        }
        None
    });

    let Ok(Some(plaintext)) = result else {
        return ptr::null_mut();
    };

    let mut boxed = plaintext.into_boxed_slice();
    let ptr = boxed.as_mut_ptr();
    *out_len = boxed.len();
    std::mem::forget(boxed);
    ptr
}

/// Frees a buffer returned by [`pheme_preview_decrypt`].
///
/// # Safety
///
/// `ptr` and `len` must be exactly what a single [`pheme_preview_decrypt`] call returned, and this
/// must be called at most once for each.
#[no_mangle]
pub unsafe extern "C" fn pheme_preview_free(ptr: *mut u8, len: usize) {
    if ptr.is_null() || len == 0 {
        return;
    }
    // Reconstituted and dropped. The buffer held decrypted message text, which should not outlive
    // the notification it was fetched for.
    drop(Vec::from_raw_parts(ptr, len, len));
}

unsafe fn as_slice<'a>(ptr: *const u8, len: usize) -> Option<&'a [u8]> {
    if ptr.is_null() || len == 0 {
        return None;
    }
    Some(slice::from_raw_parts(ptr, len))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::vault::vault_seal;
    use pheme_mls::Client;

    const GID: &[u8] = b"grp-cabi-test";

    /// Everything the extension does, in the order it does it: open the sealed state with the
    /// keychain data key, decrypt one message, hand back the plaintext.
    ///
    /// This exists because the Swift side is the one part of the chain that cannot be tested from
    /// here — so the Rust side of the boundary is pinned exactly as Swift will call it, pointers
    /// and all, and a failure in production is then a Swift bug rather than an open question.
    fn call(sealed: &[u8], key: &[u8], groups: &str, ct: &[u8]) -> Option<Vec<u8>> {
        let cgroups = std::ffi::CString::new(groups).unwrap();
        let mut out_len: usize = 0;
        unsafe {
            let p = pheme_preview_decrypt(
                sealed.as_ptr(),
                sealed.len(),
                key.as_ptr(),
                key.len(),
                cgroups.as_ptr(),
                ct.as_ptr(),
                ct.len(),
                &mut out_len,
            );
            if p.is_null() {
                return None;
            }
            let copy = slice::from_raw_parts(p, out_len).to_vec();
            pheme_preview_free(p, out_len);
            Some(copy)
        }
    }

    /// Alice and Bob in a group, Bob's state sealed exactly as MlsStore seals it on disk.
    fn fixture() -> (Client, Client, Vec<u8>, Vec<u8>) {
        let alice = Client::new("alice", "dev-a").unwrap();
        let bob = Client::new("bob", "dev-b").unwrap();
        alice.create_group(GID).unwrap();
        let staged = alice.stage_add(GID, &[bob.key_package().unwrap()]).unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.join_from_welcome(&staged.welcome).unwrap();

        let key = vec![7u8; 32];
        let sealed = vault_seal(
            STATE_DOMAIN.to_string(),
            key.clone(),
            bob.export_state().unwrap(),
        )
        .unwrap();
        (alice, bob, key, sealed)
    }

    #[test]
    fn decrypts_a_message_and_leaves_the_real_client_able_to_read_it() {
        let (alice, bob, key, sealed) = fixture();
        let ct = alice
            .encrypt(GID, br#"{"body":"hello from the lock screen"}"#)
            .unwrap();

        let got = call(&sealed, &key, "grp-cabi-test", &ct).expect("extension should decrypt");
        assert_eq!(&got, br#"{"body":"hello from the lock screen"}"#);

        // The whole safety property, at the FFI boundary: the app still reads it for real.
        assert_eq!(
            bob.decrypt(GID, &ct).unwrap().as_deref(),
            Some(&b"{\"body\":\"hello from the lock screen\"}"[..]),
            "the extension consumed the key the app needed — every previewed message would render \
             blank in the app, permanently",
        );
    }

    #[test]
    fn tries_every_group_a_conversation_has_had() {
        let (alice, _bob, key, sealed) = fixture();
        let ct = alice
            .encrypt(GID, br#"{"body":"found on the second id"}"#)
            .unwrap();
        // A retired group first, the live one second — the order the store hands them over.
        let got = call(&sealed, &key, "grp-retired\ngrp-cabi-test", &ct);
        assert_eq!(
            got.as_deref(),
            Some(&br#"{"body":"found on the second id"}"#[..])
        );
    }

    #[test]
    fn a_wrong_data_key_returns_nothing_rather_than_failing() {
        let (alice, _bob, _key, sealed) = fixture();
        let ct = alice.encrypt(GID, br#"{"body":"secret"}"#).unwrap();
        assert!(call(&sealed, &[9u8; 32], "grp-cabi-test", &ct).is_none());
    }

    #[test]
    fn a_commit_is_refused_so_the_epoch_cannot_move() {
        let (alice, bob, key, sealed) = fixture();
        let carol = Client::new("carol", "dev-c").unwrap();
        let staged = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();

        assert!(
            call(&sealed, &key, "grp-cabi-test", &staged.commit).is_none(),
            "a commit has nothing to preview and must never be merged here",
        );
        // Bob has still never seen it, so applying it for real works.
        bob.apply_commit(GID, &staged.commit).unwrap();
        assert_eq!(bob.member_identities(GID).unwrap().len(), 3);
    }

    #[test]
    fn null_and_empty_inputs_are_survivable() {
        let mut out_len: usize = 12345;
        unsafe {
            assert!(pheme_preview_decrypt(
                ptr::null(),
                0,
                ptr::null(),
                0,
                ptr::null(),
                ptr::null(),
                0,
                &mut out_len
            )
            .is_null());
            assert_eq!(
                out_len, 0,
                "out_len must be cleared even on the failure path"
            );
            // Freeing nothing is a no-op rather than a crash.
            pheme_preview_free(ptr::null_mut(), 0);
        }
    }
}
