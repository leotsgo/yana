package com.collinpendleton.yana.ui

import androidx.compose.animation.AnimatedContentTransitionScope.SlideDirection
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.normalizeServerUrl
import com.collinpendleton.yana.ui.screens.AccountScreen
import com.collinpendleton.yana.ui.screens.ActivityScreen
import com.collinpendleton.yana.ui.screens.ConflictScreen
import com.collinpendleton.yana.ui.screens.ConflictsScreen
import com.collinpendleton.yana.ui.screens.DataScreen
import com.collinpendleton.yana.ui.screens.DeletedNotesScreen
import com.collinpendleton.yana.ui.screens.NoteHistoryScreen
import com.collinpendleton.yana.ui.screens.NoteScreen
import com.collinpendleton.yana.ui.screens.PeopleScreen
import com.collinpendleton.yana.ui.screens.SearchScreen
import com.collinpendleton.yana.ui.screens.ServerScreen
import com.collinpendleton.yana.ui.screens.SettingsScreen
import com.collinpendleton.yana.ui.screens.SignInScreen
import com.collinpendleton.yana.ui.screens.SpaceScreen
import com.collinpendleton.yana.ui.screens.SpaceSettingsScreen
import com.collinpendleton.yana.ui.screens.SpacesScreen
import com.collinpendleton.yana.ui.screens.SpacesSettingsScreen
import com.collinpendleton.yana.ui.screens.TasksScreen
import kotlinx.serialization.Serializable

@Serializable data object ServerRoute
@Serializable data class SignInRoute(val server: String, val setup: Boolean)
@Serializable data object SpacesRoute
@Serializable data class SpaceRoute(val name: String, val label: String)
/** [line] is the body line a tasks row opens the note at; -1 opens at the top. [edit] opens straight into the editor. */
@Serializable data class NoteRoute(val id: String, val title: String, val line: Int = -1, val edit: Boolean = false)
@Serializable data class SearchRoute(val query: String = "")
@Serializable data class TasksRoute(val space: String = "")
@Serializable data class NoteHistoryRoute(val id: String, val title: String = "")
/** [space] is "" for the feed across every space. */
@Serializable data class ActivityRoute(val space: String = "")
@Serializable data object DeletedNotesRoute
/** Every conflict copy in the account's spaces, the Data page's list. */
@Serializable data object ConflictsRoute
/** One note's conflict copies and their resolutions; [id] is the surviving note. */
@Serializable data class ConflictRoute(val id: String, val title: String = "")
@Serializable data object SettingsRoute
/** The account as a phone handles it: password, devices, revoke. */
@Serializable data object AccountRoute
/** Every space with the role this account holds in it; 12r. */
@Serializable data object SpacesSettingsRoute
/** One space's sharing: label and members for its owner. */
@Serializable data class SpaceSettingsRoute(val space: String, val label: String)
/** The server's accounts, the owner's view; 12r. */
@Serializable data object PeopleRoute
/** Deleted notes, conflicts, and a space as a zip; 12r. */
@Serializable data object DataRoute

private const val NAV_MS = 180

@Composable
fun YanaNavHost(app: YanaApp, nav: NavHostController = rememberNavController()) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    val start: Any = remember { if (session != null) SpacesRoute else ServerRoute }

    // A session that ends underneath the shell (revoked on the web, signed
    // out, expired) sends the app back to the start with nothing behind it.
    LaunchedEffect(session == null) {
        if (session != null) return@LaunchedEffect
        val here = nav.currentBackStackEntry?.destination?.route ?: return@LaunchedEffect
        if (here.contains("ServerRoute") || here.contains("SignInRoute")) return@LaunchedEffect
        nav.navigate(ServerRoute) { popUpTo(0) { inclusive = true } }
        client.lastServer?.let(::normalizeServerUrl)?.let { nav.navigate(SignInRoute(it.toString(), setup = false)) }
    }

    // Navigation's default is a 700 ms crossfade on every push and pop,
    // which reads as lag; a short slide keeps the direction readable.
    NavHost(
        nav,
        startDestination = start,
        enterTransition = { slideIntoContainer(SlideDirection.Start, tween(NAV_MS)) + fadeIn(tween(NAV_MS)) },
        exitTransition = { fadeOut(tween(NAV_MS)) },
        popEnterTransition = { fadeIn(tween(NAV_MS)) },
        popExitTransition = { slideOutOfContainer(SlideDirection.End, tween(NAV_MS)) + fadeOut(tween(NAV_MS)) },
    ) {
        composable<ServerRoute> {
            ServerScreen(client) { server, setup -> nav.navigate(SignInRoute(server, setup)) }
        }
        composable<SignInRoute> { entry ->
            val r = entry.toRoute<SignInRoute>()
            SignInScreen(
                client = client,
                server = r.server,
                setup = r.setup,
                onBack = { if (!nav.popBackStack()) nav.navigate(ServerRoute) { popUpTo(0) { inclusive = true } } },
                onSignedIn = { nav.navigate(SpacesRoute) { popUpTo(0) { inclusive = true } } },
            )
        }
        composable<SpacesRoute> {
            SpacesScreen(
                app = app,
                onSpace = { nav.navigate(SpaceRoute(it.name, it.displayName)) },
                onSearch = { nav.navigate(SearchRoute()) },
                onSettings = { nav.navigate(SettingsRoute) },
                onTasks = { nav.navigate(TasksRoute()) },
                onActivity = { nav.navigate(ActivityRoute()) },
                onNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
            )
        }
        composable<SpaceRoute> { entry ->
            val r = entry.toRoute<SpaceRoute>()
            SpaceScreen(
                repo = app.repo,
                space = r.name,
                label = r.label,
                onBack = { nav.popBackStack() },
                onNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
                onActivity = { nav.navigate(ActivityRoute(r.name)) },
            )
        }
        composable<NoteRoute> { entry ->
            val r = entry.toRoute<NoteRoute>()
            NoteScreen(
                repo = app.repo,
                sync = app.syncEngine,
                id = r.id,
                title = r.title,
                atLine = r.line,
                startEditing = r.edit,
                onBack = { nav.popBackStack() },
                onOpenNote = { id -> nav.navigate(NoteRoute(id, "")) },
                // Until the tag page lands (12k), a tag opens its search.
                onTag = { tag -> nav.navigate(SearchRoute(query = "tag:$tag")) },
                onHistory = { id, title -> nav.navigate(NoteHistoryRoute(id, title)) },
                onConflicts = { id, title -> nav.navigate(ConflictRoute(id, title)) },
            )
        }
        composable<NoteHistoryRoute> { entry ->
            val r = entry.toRoute<NoteHistoryRoute>()
            NoteHistoryScreen(
                repo = app.repo,
                id = r.id,
                title = r.title,
                onBack = { nav.popBackStack() },
            )
        }
        composable<ActivityRoute> { entry ->
            val r = entry.toRoute<ActivityRoute>()
            val session by app.client.session.collectAsStateWithLifecycle()
            ActivityScreen(
                repo = app.repo,
                prefs = app.prefs,
                isOwner = session?.isOwner ?: false,
                initialSpace = r.space,
                onBack = { nav.popBackStack() },
                onNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
            )
        }
        composable<DeletedNotesRoute> {
            DeletedNotesScreen(
                repo = app.repo,
                onBack = { nav.popBackStack() },
                onOpenNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
            )
        }
        composable<ConflictsRoute> {
            ConflictsScreen(
                repo = app.repo,
                onBack = { nav.popBackStack() },
                onResolve = { id, title -> nav.navigate(ConflictRoute(id, title)) },
                onOpenNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
            )
        }
        composable<ConflictRoute> { entry ->
            val r = entry.toRoute<ConflictRoute>()
            ConflictScreen(
                repo = app.repo,
                id = r.id,
                title = r.title,
                onBack = { nav.popBackStack() },
            )
        }
        composable<SearchRoute> { entry ->
            val r = entry.toRoute<SearchRoute>()
            SearchScreen(
                repo = app.repo,
                initialQuery = r.query,
                onBack = { nav.popBackStack() },
                onNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
            )
        }
        composable<TasksRoute> { entry ->
            val r = entry.toRoute<TasksRoute>()
            TasksScreen(
                repo = app.repo,
                sync = app.syncEngine,
                initialSpace = r.space,
                onBack = { nav.popBackStack() },
                onNote = { id, title, line -> nav.navigate(NoteRoute(id, title, line)) },
            )
        }
        composable<SettingsRoute> {
            SettingsScreen(
                app = app,
                onBack = { nav.popBackStack() },
                onAccount = { nav.navigate(AccountRoute) },
                onSpacesSettings = { nav.navigate(SpacesSettingsRoute) },
                onPeople = { nav.navigate(PeopleRoute) },
                onData = { nav.navigate(DataRoute) },
                onOpenNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
            )
        }
        composable<AccountRoute> {
            AccountScreen(
                app = app,
                onBack = { nav.popBackStack() },
            )
        }
        composable<SpacesSettingsRoute> {
            SpacesSettingsScreen(
                app = app,
                onBack = { nav.popBackStack() },
                onSpace = { name, label -> nav.navigate(SpaceSettingsRoute(name, label)) },
            )
        }
        composable<SpaceSettingsRoute> { entry ->
            val r = entry.toRoute<SpaceSettingsRoute>()
            SpaceSettingsScreen(
                app = app,
                space = r.space,
                label = r.label,
                onBack = { nav.popBackStack() },
            )
        }
        composable<PeopleRoute> {
            PeopleScreen(
                app = app,
                onBack = { nav.popBackStack() },
            )
        }
        composable<DataRoute> {
            DataScreen(
                app = app,
                onBack = { nav.popBackStack() },
                onDeletedNotes = { nav.navigate(DeletedNotesRoute) },
                onConflicts = { nav.navigate(ConflictsRoute) },
            )
        }
    }
}
