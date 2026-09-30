package com.collinpendleton.yana.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.shape.RoundedCornerShape
import com.collinpendleton.yana.R

// The Identity palette, the same tokens as the web client (web/src/app.css):
// warm off-white, near-black ink, one amber accent.
private val Light = lightColorScheme(
    primary = Color(0xFFB8691E),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFF6E6D2),
    onPrimaryContainer = Color(0xFF9A5614),
    secondary = Color(0xFF5B564E),
    onSecondary = Color(0xFFFAF7F2),
    secondaryContainer = Color(0xFFEBE6DC),
    onSecondaryContainer = Color(0xFF1D1B18),
    tertiary = Color(0xFF9A5614),
    onTertiary = Color(0xFFFFFFFF),
    background = Color(0xFFFAF7F2),
    onBackground = Color(0xFF1D1B18),
    surface = Color(0xFFFAF7F2),
    onSurface = Color(0xFF1D1B18),
    surfaceVariant = Color(0xFFF3EFE7),
    onSurfaceVariant = Color(0xFF5B564E),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF6F2EB),
    surfaceContainer = Color(0xFFF3EFE7),
    surfaceContainerHigh = Color(0xFFEFEAE1),
    surfaceContainerHighest = Color(0xFFEBE6DC),
    outline = Color(0xFFD3CCBE),
    outlineVariant = Color(0xFFE2DCD0),
    error = Color(0xFF9A3412),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFF7E5DC),
    onErrorContainer = Color(0xFF9A3412),
    scrim = Color(0x471D1B18),
)

private val Dark = darkColorScheme(
    primary = Color(0xFFD9893B),
    onPrimary = Color(0xFF1C1A17),
    primaryContainer = Color(0xFF3A2914),
    onPrimaryContainer = Color(0xFFE9A35E),
    secondary = Color(0xFFB4AC9E),
    onSecondary = Color(0xFF1C1A17),
    secondaryContainer = Color(0xFF2C2821),
    onSecondaryContainer = Color(0xFFECE7DD),
    tertiary = Color(0xFFE9A35E),
    onTertiary = Color(0xFF1C1A17),
    background = Color(0xFF1C1A17),
    onBackground = Color(0xFFECE7DD),
    surface = Color(0xFF1C1A17),
    onSurface = Color(0xFFECE7DD),
    surfaceVariant = Color(0xFF232019),
    onSurfaceVariant = Color(0xFFB4AC9E),
    surfaceContainerLowest = Color(0xFF171512),
    surfaceContainerLow = Color(0xFF201D18),
    surfaceContainer = Color(0xFF232019),
    surfaceContainerHigh = Color(0xFF28241E),
    surfaceContainerHighest = Color(0xFF2C2821),
    outline = Color(0xFF433E35),
    outlineVariant = Color(0xFF322E27),
    error = Color(0xFFE0805E),
    onError = Color(0xFF1C1A17),
    errorContainer = Color(0xFF3B2016),
    onErrorContainer = Color(0xFFE0805E),
    scrim = Color(0x8C000000),
)

/** The UI face: Source Sans 3, a humanist sans. */
val Sans = FontFamily(
    Font(R.font.source_sans_regular, FontWeight.Normal),
    Font(R.font.source_sans_medium, FontWeight.Medium),
    Font(R.font.source_sans_semibold, FontWeight.SemiBold),
    Font(R.font.source_sans_semibold, FontWeight.Bold),
)

/** Mono is for the wordmark and code only: Source Code Pro, Source Sans's companion. */
val Mono = FontFamily(
    Font(R.font.source_code_regular, FontWeight.Normal),
    Font(R.font.source_code_semibold, FontWeight.SemiBold),
    Font(R.font.source_code_semibold, FontWeight.Bold),
)

private fun sans(size: Int, line: Int, weight: FontWeight = FontWeight.Normal, tracking: Double = 0.0) = TextStyle(
    fontFamily = Sans,
    fontWeight = weight,
    fontSize = size.sp,
    lineHeight = line.sp,
    letterSpacing = tracking.sp,
)

private val Type = Typography(
    displayLarge = sans(52, 60),
    displayMedium = sans(42, 50),
    displaySmall = sans(34, 42),
    headlineLarge = sans(30, 38, FontWeight.SemiBold),
    headlineMedium = sans(26, 34, FontWeight.SemiBold),
    headlineSmall = sans(22, 30, FontWeight.SemiBold),
    titleLarge = sans(21, 28, FontWeight.SemiBold),
    titleMedium = sans(17, 24, FontWeight.SemiBold),
    titleSmall = sans(15, 21, FontWeight.SemiBold),
    bodyLarge = sans(17, 25),
    bodyMedium = sans(15, 22),
    bodySmall = sans(13, 18),
    labelLarge = sans(15, 20, FontWeight.SemiBold),
    labelMedium = sans(13, 18, FontWeight.Medium),
    labelSmall = sans(12, 16, FontWeight.Medium, 0.2),
)

private val Shape = Shapes(
    extraSmall = RoundedCornerShape(5.dp),
    small = RoundedCornerShape(5.dp),
    medium = RoundedCornerShape(8.dp),
    large = RoundedCornerShape(12.dp),
    extraLarge = RoundedCornerShape(16.dp),
)

/** How the app picks light or dark; the settings screen sets it. */
enum class ThemeMode { System, Light, Dark }

@Composable
fun YanaTheme(mode: ThemeMode = ThemeMode.System, content: @Composable () -> Unit) {
    val dark = when (mode) {
        ThemeMode.System -> isSystemInDarkTheme()
        ThemeMode.Light -> false
        ThemeMode.Dark -> true
    }
    MaterialTheme(colorScheme = if (dark) Dark else Light, typography = Type, shapes = Shape, content = content)
}
