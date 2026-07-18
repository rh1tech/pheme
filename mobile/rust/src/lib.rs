pub mod api;
/// The C ABI for the iOS NotificationServiceExtension. Not under `api/`: everything there is
/// scanned by flutter_rust_bridge's codegen, and this is deliberately not a Dart binding.
pub mod cabi;
mod frb_generated;
