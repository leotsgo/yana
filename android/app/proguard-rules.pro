# kotlinx.serialization and Retrofit ship their own consumer rules; the
# API interfaces only need their generic signatures kept.
-keepattributes Signature, InnerClasses, EnclosingMethod, *Annotation*
-keep,allowobfuscation,allowshrinking interface com.collinpendleton.yana.data.YanaApi
-keep,allowobfuscation,allowshrinking interface com.collinpendleton.yana.data.AuthApi

# Tink (under security-crypto) references compile-only annotations.
-dontwarn com.google.errorprone.annotations.**
-dontwarn javax.annotation.**
