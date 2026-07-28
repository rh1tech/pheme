import java.io.FileInputStream
import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// Release signing credentials, loaded from the gitignored android/key.properties.
val keystoreProperties = Properties()
val keystorePropertiesFile = rootProject.file("key.properties")
if (keystorePropertiesFile.exists()) {
    keystoreProperties.load(FileInputStream(keystorePropertiesFile))
}

// Firebase Cloud Messaging: apply the google-services plugin only when the
// config file is present, so the app still builds without Firebase configured.
// Drop android/app/google-services.json in and rebuild to enable push.
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
}

android {
    namespace = "tech.rh1.pheme.pheme_mobile"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        // Required by flutter_local_notifications (uses java.time APIs).
        isCoreLibraryDesugaringEnabled = true
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "tech.rh1.pheme.pheme_mobile"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        create("release") {
            if (keystorePropertiesFile.exists()) {
                keyAlias = keystoreProperties["keyAlias"] as String
                keyPassword = keystoreProperties["keyPassword"] as String
                storeFile = file(keystoreProperties["storeFile"] as String)
                storePassword = keystoreProperties["storePassword"] as String
            }
        }
    }

    buildTypes {
        release {
            // Use the real upload key when key.properties is present; fall back
            // to debug keys otherwise (so `flutter run --release` still works in
            // a checkout without the keystore).
            signingConfig = if (keystorePropertiesFile.exists()) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

// Pins Cronet below the release that broke the manifest merge.
//
// Google published play-services-cronet 18.1.1, which jumped from cronet-api 72 to cronet 141 —
// and 141 is where Cronet was split into cronet-api PLUS a new cronet-shared, both declaring the
// namespace org.chromium.net. AGP validates namespace uniqueness across libraries, so the merge
// stops dead:
//
//   Namespace 'org.chromium.net' is used in multiple modules and/or libraries:
//     org.chromium.net:cronet-api:141.7340.3, org.chromium.net:cronet-shared:141.7340.3
//
// Nothing in this repo asked for that upgrade and nothing here can be fixed to avoid it. The whole
// chain is transitive — cronet_http, via native_dio_adapter, which is what gives Dio the platform's
// native HTTP stack — and it names play-services-cronet with no upper bound, so Gradle took the new
// release and `flutter build apk --release` stopped producing an APK. A supply-chain break, and one
// that CI would not have caught: it only runs analyze and test, never a release build.
//
// 18.1.0 is the last release before the split. Drop this pin once the two artifacts have distinct
// namespaces, and check that a release APK still builds when you do.
configurations.all {
    resolutionStrategy {
        force("com.google.android.gms:play-services-cronet:18.1.0")
    }
}

dependencies {
    coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.4")
    // ShortcutManagerCompat and friends, for the conversation shortcuts that let Android promote a
    // message notification to a conversation — avatar in the icon slot, app icon badged onto it.
    implementation("androidx.core:core-ktx:1.13.1")
}
