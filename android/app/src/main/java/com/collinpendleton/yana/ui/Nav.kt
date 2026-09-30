package com.collinpendleton.yana.ui

import android.widget.Toast
import androidx.activity.compose.LocalActivity
import androidx.compose.animation.AnimatedContentTransitionScope.SlideDirection
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.windowsizeclass.WindowWidthSizeClass
import androidx.compose.material3.windowsizeclass.calculateWindowSizeClass
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isAltPressed
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
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
import com.collinpendleton.yana.ui.screens.NewNoteScreen
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
import com.collinpendleton.yana.ui.screens.TrashScreen
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
/** The new-note picker: [start] names the folder being viewed when the
 * New tab was pressed from the tree; the picker falls back to the
 * folder this device used last. */
@Serializable data class NewNoteRoute(val start: String = "")
/** Deleted notes with their ways out: restore, destroy, empty. */
@Serializable data object TrashRoute
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

/** A note the detail side holds, as saveable strings. */
private val NoteSpecSaver: Saver<NoteSpec?, List<String>> = Saver(
    save = { it?.let(::encodeNoteSpec) },
    restore = { decodeNoteSpec(it) },
)

/**
 * The shell: the navigation stack with the phone's bottom bar under
 * it. The bar carries the web's phone set — Notes (the tree), Search,
 * Capture, Today, Tasks with the open count, New — and steps out of
 * the way on the screens that are not its own and while a note is
 * being edited.
 *
 * On a medium or expanded width — a tablet, a foldable opened flat, a
 * Chromebook — the shell goes two-pane instead, the way the web does
 * at tablet size: the list screen (the tree, search, tasks, tags)
 * stays on the left and the note it picks opens on the right, and the
 * bar steps out of the way entirely, as the web's does past its phone
 * layout. At expanded width the note side itself splits over a
 * draggable divider (see [DetailPane]).
 */
@OptIn(androidx.compose.material3.windowsizeclass.ExperimentalMaterial3WindowSizeClassApi::class)
@Composable
fun YanaNavHost(app: YanaApp, nav: NavHostController = rememberNavController()) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    val start: Any = remember { if (session != null) SpacesRoute else ServerRoute }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val editingPanes by ShellState.editingPanes.collectAsStateWithLifecycle()
    val editing = editingPanes.isNotEmpty()

    // The window's width class: compact keeps the phone layout; medium
    // and expanded go list-detail; expanded also allows two notes.
    val activity = LocalActivity.current
    val width = activity?.let { calculateWindowSizeClass(it) }
    val wide = width != null && width.widthSizeClass != WindowWidthSizeClass.Compact
    val expanded = width?.widthSizeClass == WindowWidthSizeClass.Expanded

    // What the detail side holds, and how the note side splits when it
    // does. Both live past a rotation and a fold.
    var detail by rememberSaveable(stateSaver = NoteSpecSaver) { mutableStateOf<NoteSpec?>(null) }
    var beside by rememberSaveable(stateSaver = NoteSpecSaver) { mutableStateOf<NoteSpec?>(null) }
    var besideFraction by rememberSaveable { mutableStateOf(0.5f) }

    // The open count the Tasks tab carries; freshened as the person
    // moves around, the cached count answering offline.
    val countVm: Loader<TaskCount?> = viewModel(key = "shell-task-count") {
        Loader(fetch = { app.repo.openTaskCount() })
    }
    val countState by countVm.loaded.collectAsStateWithLifecycle()

    val backStack by nav.currentBackStackEntryAsState()
    val dest = backStack?.destination
    val showDetailPane = wide && session != null && dest.barred()
    val barShown = session != null && !editing && !wide && dest.barred()

    // A session that ends underneath the shell (revoked on the web, signed
    // out, expired) sends the app back to the start with nothing behind it.
    LaunchedEffect(session == null) {
        if (session != null) return@LaunchedEffect
        detail = null
        beside = null
        val here = nav.currentBackStackEntry?.destination?.route ?: return@LaunchedEffect
        if (here.contains("ServerRoute") || here.contains("SignInRoute")) return@LaunchedEffect
        nav.navigate(ServerRoute) { popUpTo(0) { inclusive = true } }
        client.lastServer?.let(::normalizeServerUrl)?.let { nav.navigate(SignInRoute(it.toString(), setup = false)) }
    }

    // The fold line crossed: a note route sitting on a list route folds
    // into the detail pane when the window goes wide, and the detail
    // pane's note becomes a plain route when it goes compact — the note
    // never reloads and the editor keeps its caret (NoteSessions). A
    // note pushed over a full-screen page (the feed, the trash, Help's
    // guide) stays a full-screen page, here and there.
    LaunchedEffect(wide, dest?.route) {
        if (!wide) {
            val spec = detail
            if (spec != null) {
                if (dest.barred()) nav.navigate(NoteRoute(spec.id, spec.title, spec.line, spec.edit))
                detail = null
            }
            beside = null
            return@LaunchedEffect
        }
        val topEntry = nav.currentBackStackEntry ?: return@LaunchedEffect
        if (topEntry.destination.nameOf() != NoteRoute.routeName) return@LaunchedEffect
        val top = topEntry.toRoute<NoteRoute>().let { r -> NoteSpec(r.id, r.title, r.line, r.edit) }
        // Pop down to the topmost note entry; the run folds only when
        // what it sits on is a list route.
        while (nav.previousBackStackEntry?.destination?.nameOf() == NoteRoute.routeName) {
            if (!nav.popBackStack()) return@LaunchedEffect
        }
        if (nav.previousBackStackEntry?.destination?.barred() == true) {
            nav.popBackStack()
            detail = top
        }
    }

    /** Opens a note: the detail pane when the shell is list-detail, a route otherwise. */
    fun openNote(id: String, title: String, line: Int = -1, edit: Boolean = false, inDetail: Boolean = wide && dest.barred()) {
        if (inDetail) {
            detail = NoteSpec(id, title, line, edit)
        } else {
            nav.navigate(NoteRoute(id, title, line, edit))
        }
    }

    fun openToday() {
        scope.launch {
            val today = app.capture.todayNote()
            if (today == null) {
                Toast.makeText(context, app.getString(R.string.today_failed), Toast.LENGTH_SHORT).show()
            } else {
                openNote(today.id, today.path.substringAfterLast('/').removeSuffix(".md"))
            }
        }
    }

    fun newNote() {
        // The picker starts in the folder being viewed — the space the
        // tree was opened for — and falls back to the folder this
        // device used last. Only the in-app New goes through it; the
        // quick entries (tile, widget, share) keep the inbox path.
        val start = if (dest.nameOf() == SpaceRoute.routeName) {
            backStack?.toRoute<SpaceRoute>()?.space.orEmpty()
        } else {
            ""
        }
        nav.navigate(NewNoteRoute(start))
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

    // The hardware keyboard's shortcuts, the web's set on the keys an
    // app can take: Ctrl+P the switcher, Ctrl+T a new note, Ctrl+F
    // search. The Ctrl pairs are read on the way down, so they work
    // with the caret in a field too.
    fun onPreviewKey(e: androidx.compose.ui.input.key.KeyEvent): Boolean {
        if (e.type != KeyEventType.KeyDown || session == null || !e.isCtrlPressed || e.isAltPressed || e.isMetaPressed) return false
        return when (e.key) {
            Key.P -> { nav.navigate(SwitcherRoute); true }
            Key.T -> { newNote(); true }
            Key.F -> { nav.navigate(SearchRoute()) { launchSingleTop = true }; true }
            else -> false
        }
    }

    // The bare keys arrive only when nothing under them took them: E
    // flips the note on screen between reading and editing, Escape
    // finishes an editor, closes the detail pane, or goes back.
    fun onKey(e: androidx.compose.ui.input.key.KeyEvent): Boolean {
        if (e.type != KeyEventType.KeyDown || session == null) return false
        val plain = !e.isCtrlPressed && !e.isAltPressed && !e.isMetaPressed
        return when {
            e.key == Key.E && plain && !e.isShiftPressed -> when {
                showDetailPane && detail != null -> { ShellState.requestEdit("detail"); true }
                dest.nameOf() == NoteRoute.routeName -> { ShellState.requestEdit("route"); true }
                else -> false
            }
            e.key == Key.Escape -> when {
                editingPanes.isNotEmpty() -> {
                    editingPanes.forEach(ShellState::requestEditDone)
                    true
                }
                showDetailPane && detail != null -> { detail = null; true }
                else -> nav.popBackStack()
            }
            else -> false
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .onPreviewKeyEvent(::onPreviewKey)
            .onKeyEvent(::onKey),
    ) {
        Row(Modifier.weight(1f).fillMaxWidth()) {
            NavHost(
                nav,
                startDestination = start,
                modifier = Modifier.weight(1f).fillMaxHeight(),
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
                    onNote = { id, title -> openNote(id, title) },
                    onSwitcher = { nav.navigate(SwitcherRoute) },
                    onTags = { nav.navigate(TagsRoute) },
                )
            }
            composable<SpaceRoute> { entry ->
                val r = entry.toRoute<SpaceRoute>()
                SpaceScreen(
                    repo = app.repo,
                    prefs = app.prefs,
                    capture = app.capture,
                    focus = r.space,
                    label = r.label,
                    onBack = { if (!nav.popBackStack()) nav.navigate(SpacesRoute) },
                    onNote = { id, title -> openNote(id, title) },
                    onNewNote = { id, title -> openNote(id, title, edit = true) },
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
                        nav.popBackStack(SwitcherRoute.routeName, true)
                        openNote(id, title, inDetail = wide)
                    },
                    onCreate = { name ->
                        scope.launch {
                            val note = app.capture.newNamedNote(name)
                            if (note == null) {
                                Toast.makeText(context, app.getString(R.string.capture_no_space), Toast.LENGTH_SHORT).show()
                            } else {
                                nav.popBackStack(SwitcherRoute.routeName, true)
                                openNote(note.id, note.title, edit = true, inDetail = wide)
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
                    onNote = { id, title -> openNote(id, title) },
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
            composable<NewNoteRoute> { entry ->
                val r = entry.toRoute<NewNoteRoute>()
                NewNoteScreen(
                    app = app,
                    repo = app.repo,
                    prefs = app.prefs,
                    start = r.start,
                    onBack = { nav.popBackStack() },
                    onOpen = { id, title ->
                        nav.popBackStack(NewNoteRoute.routeName, true)
                        openNote(id, title, inDetail = wide)
                    },
                    onCreated = { id, title ->
                        nav.popBackStack(NewNoteRoute.routeName, true)
                        openNote(id, title, edit = true, inDetail = wide)
                    },
                )
            }
            composable<TrashRoute> {
                TrashScreen(
                    repo = app.repo,
                    onBack = { nav.popBackStack() },
                    onOpenNote = { id, title -> nav.navigate(NoteRoute(id, title)) },
                    onDeletedNotes = { nav.navigate(DeletedNotesRoute) },
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
                    onNote = { id, title -> openNote(id, title) },
                )
            }
            composable<TasksRoute> { entry ->
                val r = entry.toRoute<TasksRoute>()
                TasksScreen(
                    repo = app.repo,
                    sync = app.syncEngine,
                    initialSpace = r.space,
                    onBack = { nav.popBackStack() },
                    onNote = { id, title, line -> openNote(id, title, line) },
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
                    onTrash = { nav.navigate(TrashRoute) },
                    onConflicts = { nav.navigate(ConflictsRoute) },
                )
            }
        }
        if (showDetailPane) {
            // The line the web draws between its sidebar and its content.
            Box(Modifier.width(1.dp).fillMaxHeight().background(MaterialTheme.colorScheme.outlineVariant))
            Box(Modifier.weight(1.4f).fillMaxHeight()) {
                val spec = detail
                if (spec != null) {
                    DetailPane(
                        app = app,
                        main = spec,
                        onMain = { detail = it },
                        beside = beside,
                        onBeside = { beside = it },
                        fraction = besideFraction,
                        onFraction = { besideFraction = it },
                        splitAllowed = expanded,
                        onTag = { tag -> nav.navigate(TagRoute(tag)) },
                        onHistory = { id, title -> nav.navigate(NoteHistoryRoute(id, title)) },
                        onConflicts = { id, title -> nav.navigate(ConflictRoute(id, title)) },
                        onClose = { detail = null },
                        modifier = Modifier.fillMaxSize(),
                    )
                } else {
                    DetailPlaceholder(Modifier.fillMaxSize())
                }
            }
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
