package com.collinpendleton.yana.ui

import android.widget.Toast
import androidx.compose.animation.AnimatedContentTransitionScope.SlideDirection
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavDestination
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.TaskCount
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
import com.collinpendleton.yana.ui.screens.SpacesScreen
import com.collinpendleton.yana.ui.screens.SpacesSettingsScreen
import com.collinpendleton.yana.ui.screens.SpaceSettingsScreen
import com.collinpendleton.yana.ui.screens.SwitcherScreen
import com.collinpendleton.yana.ui.screens.TagScreen
import com.collinpendleton.yana.ui.screens.TagsScreen
import com.collinpendleton.yana.ui.screens.TasksScreen
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable

@Serializable data object ServerRoute
@Serializable data class SignInRoute(val server: String, val setup: Boolean)
@Serializable data object SpacesRoute
/** The tree: every space when [space] is "", one space focused when it names one. */
@Serializable data class SpaceRoute(val space: String = "", val label: String = "")
/** [line] is the body line a tasks row opens the note at; -1 opens at the top. [edit] opens straight into the editor. */
@Serializable data class NoteRoute(val id: String, val title: String, val line: Int = -1, val edit: Boolean = false)
@Serializable data class SearchRoute(val query: String = "")
@Serializable data object SwitcherRoute
@Serializable data object TagsRoute
@Serializable data class TagRoute(val tag: String)
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

/** The name a type-safe destination carries, without its argument tail. */
private fun NavDestination?.nameOf(): String? = this?.route?.substringBefore('?')

/** The name a route object carries, as its destination is known by. */
private val Any.routeName: String get() = this::class.qualifiedName!!

/**
 * The shell: the navigation stack with the phone's bottom bar under
 * it. The bar carries the web's phone set — Notes (the tree), Search,
 * Capture, Today, Tasks with the open count, New — and steps out of
 * the way on the screens that are not its own and while a note is
 * being edited.
 */
@Composable
fun YanaNavHost(app: YanaApp, nav: NavHostController = rememberNavController()) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    val start: Any = remember { if (session != null) SpacesRoute else ServerRoute }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val editing by ShellState.editing.collectAsStateWithLifecycle()

    // The open count the Tasks tab carries; freshened as the person
    // moves around, the cached count answering offline.
    val countVm: Loader<TaskCount?> = viewModel(key = "shell-task-count") {
        Loader(fetch = { app.repo.openTaskCount() })
    }
    val countState by countVm.loaded.collectAsStateWithLifecycle()

    val backStack by nav.currentBackStackEntryAsState()
    val dest = backStack?.destination
    val barShown = session != null && !editing && dest.barred()

    // A session that ends underneath the shell (revoked on the web, signed
    // out, expired) sends the app back to the start with nothing behind it.
    LaunchedEffect(session == null) {
        if (session != null) return@LaunchedEffect
        val here = nav.currentBackStackEntry?.destination?.route ?: return@LaunchedEffect
        if (here.contains("ServerRoute") || here.contains("SignInRoute")) return@LaunchedEffect
        nav.navigate(ServerRoute) { popUpTo(0) { inclusive = true } }
        client.lastServer?.let(::normalizeServerUrl)?.let { nav.navigate(SignInRoute(it.toString(), setup = false)) }
    }

    fun openToday() {
        scope.launch {
            val today = app.capture.todayNote()
            if (today == null) {
                Toast.makeText(context, app.getString(R.string.today_failed), Toast.LENGTH_SHORT).show()
            } else {
                nav.navigate(NoteRoute(today.id, today.path.substringAfterLast('/').removeSuffix(".md")))
            }
        }
    }

    fun newNote() {
        scope.launch {
            val note = app.capture.newInboxNote()
            if (note == null) {
                Toast.makeText(context, app.getString(R.string.capture_no_space), Toast.LENGTH_SHORT).show()
            } else {
                nav.navigate(NoteRoute(note.id, note.title, edit = true))
            }
        }
    }

    var captureOpen by remember { mutableStateOf(false) }

    fun onTab(tab: BottomTab) {
        when (tab) {
            BottomTab.Notes -> {
                // The web's Notes button toggles the tree drawer; on a
                // phone the tree is a screen, so the button opens it and
                // a second tap walks back home.
                if (dest.nameOf() == SpaceRoute.routeName) {
                    if (!nav.popBackStack(SpacesRoute, false)) nav.navigate(SpacesRoute)
                } else {
                    nav.navigate(SpaceRoute()) { launchSingleTop = true }
                }
            }
            BottomTab.Search -> nav.navigate(SearchRoute()) { launchSingleTop = true }
            BottomTab.Capture -> captureOpen = true
            BottomTab.Today -> openToday()
            BottomTab.Tasks -> nav.navigate(TasksRoute()) { launchSingleTop = true }
            BottomTab.New -> newNote()
        }
    }

    Column(Modifier.fillMaxSize()) {
        NavHost(
            nav,
            startDestination = start,
            modifier = Modifier.weight(1f),
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
                    onSwitcher = { nav.navigate(SwitcherRoute) },
                    onTags = { nav.navigate(TagsRoute) },
                )
            }
            composable<SpaceRoute> { entry ->
                val r = entry.toRoute<SpaceRoute>()
                SpaceScreen(
                    repo = app.repo,
                    prefs = app.prefs,
                    focus = r.space,
                    label = r.label,
                    onBack = { if (!nav.popBackStack()) nav.navigate(SpacesRoute) },
                    onNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
                    onActivity = { nav.navigate(ActivityRoute(r.space)) },
                    onSwitcher = { nav.navigate(SwitcherRoute) },
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
                    onTag = { tag -> nav.navigate(TagRoute(tag)) },
                    onHistory = { id, title -> nav.navigate(NoteHistoryRoute(id, title)) },
                    onConflicts = { id, title -> nav.navigate(ConflictRoute(id, title)) },
                )
            }
            composable<SwitcherRoute> {
                SwitcherScreen(
                    repo = app.repo,
                    prefs = app.prefs,
                    onBack = { nav.popBackStack() },
                    onNote = { id, title ->
                        nav.navigate(NoteRoute(id, title)) { popUpTo(SwitcherRoute.routeName) { inclusive = true } }
                    },
                    onCreate = { name ->
                        scope.launch {
                            val note = app.capture.newNamedNote(name)
                            if (note == null) {
                                Toast.makeText(context, app.getString(R.string.capture_no_space), Toast.LENGTH_SHORT).show()
                            } else {
                                nav.navigate(NoteRoute(note.id, note.title, edit = true)) {
                                    popUpTo(SwitcherRoute.routeName) { inclusive = true }
                                }
                            }
                        }
                    },
                )
            }
            composable<TagsRoute> {
                TagsScreen(
                    repo = app.repo,
                    onBack = { nav.popBackStack() },
                    onTag = { tag -> nav.navigate(TagRoute(tag)) },
                )
            }
            composable<TagRoute> { entry ->
                val r = entry.toRoute<TagRoute>()
                TagScreen(
                    repo = app.repo,
                    tag = r.tag,
                    onBack = { nav.popBackStack() },
                    onAllTags = {
                        if (!nav.popBackStack(TagsRoute.routeName, false)) nav.navigate(TagsRoute)
                    },
                    onNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
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
        if (barShown) {
            val selected = when {
                dest.nameOf() == SpaceRoute.routeName -> BottomTab.Notes
                dest.nameOf() == TasksRoute.routeName -> BottomTab.Tasks
                else -> null
            }
            LaunchedEffect(dest?.route) { if (session != null) countVm.reload() }
            YanaBottomBar(
                selected = selected,
                taskCount = countState.data?.count,
                onTab = ::onTab,
            )
        }
        if (captureOpen) {
            CaptureLineDialog(app) { captureOpen = false }
        }
    }
}

/** The destinations the bottom bar belongs under, by their route names. */
private val barRoutes: Set<String> = setOf(
    SpacesRoute.routeName,
    SpaceRoute.routeName,
    NoteRoute.routeName,
    SearchRoute.routeName,
    TasksRoute.routeName,
    TagsRoute.routeName,
    TagRoute::class.qualifiedName!!,
)

private fun NavDestination?.barred(): Boolean = nameOf() in barRoutes
