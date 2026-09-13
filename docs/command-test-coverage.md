# Command test coverage

Baseline reviewed: v0.3.1, commit `8c42d8b1d2f9df091e0e76734755f33e8c807c8c`.
The command list below is generated from the baseline Cobra binary (`tg --help`)
and records repository evidence, not inferred capability. “Offline asserted”
means a test checks behavior beyond merely registering the command. “Live” is
not claimed for this baseline unless a redacted, reproducible evidence file is
listed; no private Telegram transcript is stored in this repository.

| Command | Offline asserted evidence | Live execution | Status / limitation |
| --- | --- | --- | --- |
| `account-limits` | `internal/commands/phase24_test.go:TestResolveAndAccountLimitsExposeExplicitSources` | unverified | Fake-backed shape; live app-config values remain unverified |
| `account-sessions` | `internal/commands/admin_test.go:TestAccountSessionsUsesListSessions` | unverified | Read path asserted; no live session inventory in repo |
| `accounts-add` | `internal/commands/read_only_test.go:TestAccountMutationsRejectReadOnlyWithoutFilesystemChanges` | unverified | Guard and account isolation covered |
| `accounts-list` | account selection tests | unverified | Listing behavior covered indirectly |
| `accounts-remove` | `internal/commands/accounts_command_test.go:TestAccountsRemoveUsesTypedConfirmationContract` | unverified | Destructive path prepared offline only |
| `accounts-show` | `internal/commands/read_only_global_test.go:TestAccountsShowReadOnlyDoesNotCreatePaths` | unverified | Read-only path covered |
| `accounts-use` | account selection tests | unverified | Selection precedence covered |
| `backfill` | `internal/commands/localdb_test.go` backfill cap, rollback, media, recovery, and schema tests | unverified | No live mutation/read fixture in repo |
| `backfill-entities` | `internal/commands/backfill_entities_test.go:TestRunBackfillEntitiesRejectsNumericBoundsBeforeArtifacts` | unverified | Numeric preflight covered |
| `ban-from-chat` | `internal/commands/admin_test.go:TestBanFromChatRequiresTypedUserConfirm` | unverified | Live admin fixture unavailable and mutation not authorized |
| `block-user` | `internal/commands/destructive_test.go:TestBlockUserRequiresConfirm` | unverified | Offline guard only |
| `chat-description` | admin tests cover shared invocation path | unverified | Exact client assertion not yet present |
| `chat-invite-link` | admin tests cover shared invocation path | unverified | Exact client assertion not yet present |
| `chat-members` | `internal/commands/admin_test.go:TestChatsInfoAndMembersReadCommands` | unverified | Read client invocation asserted |
| `chat-permissions` | `internal/commands/phase24_test.go:TestChatPermissionsReadUsesFakeAndMarksAdvisory` | unverified | Advisory rights only; no live forum fixture |
| `chat-photo` | admin tests cover shared invocation path | unverified | Exact client assertion not yet present |
| `chat-pinned-list` | `internal/commands/admin_test.go:TestChatsInfoAndMembersReadCommands` | unverified | Read client invocation asserted |
| `chat-title` | `internal/commands/admin_test.go:TestChatTitleInvokesClient` | unverified | Offline fake invocation asserted |
| `chats-info` | `internal/commands/admin_test.go:TestChatsInfoAndMembersReadCommands` | unverified | Read client invocation asserted |
| `completion` | Cobra registration exercised by binary help generation | unverified | Shell output not behaviorally tested |
| `db-backup` | `internal/store/backup_test.go` | unverified | Local snapshot behavior asserted |
| `db-restore` | `internal/store/backup_test.go` | unverified | Empty-destination and validation behavior asserted |
| `delete-msg` | `internal/commands/destructive_test.go` delete confirmation and execution tests; `internal/client/destructive_rpc_test.go` peer/count tests | unverified | No live deletion |
| `demote` | `internal/commands/admin_test.go:TestDemoteRequiresResolvedChatConfirmation` | unverified | Offline confirmation only |
| `discover` | `internal/commands/localdb_test.go:TestDiscoverUpsertsChats` | unverified | Fake-backed cache write asserted |
| `discussion-message` | `internal/commands/phase24_test.go` thread command coverage | unverified | Fake-backed response shape; no live linked discussion |
| `doctor` | account selection and doctor tests | unverified | Diagnostics asserted; no live report |
| `download-album` | `internal/commands/media_album_download_test.go` dry-run, partial, overwrite, recovery tests | unverified | Fake-backed local/media behavior |
| `download-media` | `internal/commands/media_download_test.go` gate, selector, artifact identity, recovery tests | unverified | Fake-backed local/media behavior |
| `edit-msg` | `internal/commands/messages_write_test.go:TestEditMsgInvokesClient` | unverified | No live edit |
| `export` | `internal/commands/export_test.go` JSONL/CSV/HTML, overwrite, manifest tests | unverified | Local-only by contract |
| `folder-add-chat` | `internal/commands/topics_folders_test.go` folder behavior and `internal/client/permissions_test.go` patch preservation | unverified | No live folder mutation |
| `folder-create` | `internal/commands/topics_folders_test.go:TestFolderCreateReplaysIdempotency` | unverified | Fake-backed idempotency |
| `folder-delete` | `internal/commands/topics_folders_test.go:TestFolderDeleteRejectsDefaultFolder` and typed confirmation test | unverified | No live folder deletion |
| `folder-edit` | folder patch preservation tests | unverified | No live folder mutation |
| `folder-remove-chat` | folder patch preservation tests | unverified | No live folder mutation |
| `folder-show` | `internal/commands/topics_folders_test.go:TestFoldersListAndShowUseClient` | unverified | Fake-backed read |
| `folders-list` | `internal/commands/topics_folders_test.go:TestFoldersListAndShowUseClient` | unverified | Fake-backed read |
| `folders-reorder` | folder patch preservation tests | unverified | No live folder mutation |
| `forward` | `internal/commands/messages_write_test.go:TestForwardInvokesClient` | unverified | No live forward |
| `get-msg` | `internal/commands/messages_read_test.go` get and deleted-row tests; remote runner tests | unverified | Defaults to cache; Telegram source is bounded and unverified live |
| `help` | Cobra help generation used by docs generator | unverified | Rendering, not command semantics |
| `kick` | `internal/client/destructive_rpc_test.go:TestKickReportsPartialCommitAndDoesNotClearExistingRestrictions` | unverified | Offline fake/TL behavior only |
| `leave-chat` | `internal/commands/destructive_test.go` user rejection and group execution | unverified | No live leave |
| `list-msgs` | `internal/commands/messages_read_test.go` date/filter tests; `internal/store/cursor_test.go` timestamp cursor test | unverified | Cache-only at baseline |
| `listen` | `internal/commands/live_test.go` event, filters, output failure; update storage tests | unverified | Controlled live event not run |
| `login` | `internal/commands/login_test.go` QR secret-output test and read-only guards | unverified | No auth mutation/live login |
| `mark-read` | `internal/commands/messages_write_test.go:TestMarkReadInvokesClient` | unverified | No live read marker |
| `me` | `internal/commands/auth_test.go` offline and envelope tests; read-only tests | unverified | Cached/live fetch seams asserted; no live identity evidence |
| `operations-list` | `internal/commands/recovery.go` plus write ledger tests | unverified | Durable outcome inspection covered through store/client tests |
| `pin-msg` | `internal/commands/messages_write_test.go:TestPinUnpinInvokesClient` | unverified | No live pin |
| `promote` | `internal/commands/admin_test.go:TestPromoteRequiresResolvedChatConfirmation` | unverified | Offline confirmation only |
| `react` | `internal/commands/messages_write_test.go:TestReactRejectsEmptyEmoji` | unverified | Big animation/Premium semantics not yet covered |
| `replies` | `internal/commands/phase24_test.go:TestRepliesRunnerBindsCursorToRootAndChat` | unverified | Fake-backed bounded page; no live thread fixture |
| `resolve` | `internal/commands/phase24_test.go:TestResolveAndAccountLimitsExposeExplicitSources` | unverified | Fake-backed typed identity; no live username evidence |
| `search` | `internal/commands/messages_read_test.go` empty/case tests; remote runner tests | unverified | Defaults to cache; Telegram source is bounded and unverified live |
| `send` | `internal/commands/messages_write_test.go` gate, dry-run, fuzzy, idempotency, topic tests | unverified | No live send |
| `send-by-username` | send username tests and selector pipeline | unverified | No live send |
| `set-permissions` | `internal/commands/admin_test.go:TestSetPermissionsAcceptsSendMessagesFlag`; `internal/client/permissions_test.go` patch tests | unverified | No live admin fixture |
| `setup` | `internal/commands/setup_test.go` credential, preservation, read-only, stable-root tests | unverified | Explicit and default destinations covered offline |
| `show` | `internal/commands/messages_read_test.go:TestShowRunnerResolverIntegration`, deleted/envelope, remote tests | unverified | Defaults to cache; Telegram source is bounded and unverified live |
| `stats` | `internal/commands/read_extra_test.go:TestStatsContactsUnreadReadFromCache` | unverified | Cache-only |
| `sync` | `internal/commands/sync_test.go` checkpoint, follow, reconnect tests | unverified | No live update stream |
| `sync-contacts` | `internal/commands/localdb_test.go:TestSyncContactsUpsertsContacts` | unverified | Fake-backed cache write |
| `terminate-session` | `internal/commands/destructive_test.go:TestTerminateSessionTypedConfirm` | unverified | No live session revocation |
| `topic-create` | `internal/commands/topics_folders_test.go:TestTopicCreateCallsClientAndReplaysIdempotency` | unverified | No live forum fixture |
| `topic-edit` | `internal/commands/topics_folders_test.go:TestTopicEditRequiresMutation` | unverified | No live forum fixture |
| `topic-pin` | topic command tests | unverified | No live forum fixture |
| `topic-unpin` | topic command tests | unverified | No live forum fixture |
| `topics-list` | topic command tests | unverified | No live forum fixture |
| `unban-from-chat` | admin tests cover shared confirmation path | unverified | Exact live denial/success absent |
| `unblock-user` | `internal/commands/destructive_test.go:TestUnblockUserExecutes` | unverified | Offline fake-backed execution |
| `unpin-msg` | `internal/commands/messages_write_test.go:TestPinUnpinInvokesClient` | unverified | No live unpin |
| `unread` | `internal/commands/read_extra_test.go:TestStatsContactsUnreadReadFromCache` | unverified | Cache-only |
| `upload-album` | `internal/commands/upload_album_test.go` extensive dry-run, order, mapping, idempotency, failure tests; `internal/client/upload_album_test.go` TL-shape tests | unverified | No live album mutation |
| `upload-document` | `internal/commands/media_test.go` invocation/dry-run/idempotency tests | unverified | No live upload |
| `upload-photo` | `internal/commands/media_test.go:TestUploadPhotoDryRunSkipsClient` | unverified | No live upload |
| `upload-video` | media tests cover shared upload path | unverified | Exact video live/fixture evidence absent |
| `upload-voice` | media tests cover shared upload path | unverified | Exact voice live/fixture evidence absent |
| `version` | `internal/commands/root_test.go` version/envelope/provenance tests | unverified | Local utility |

## Evidence policy

The matrix is intentionally conservative. A command is not marked live merely
because it can connect, and a command is not marked behaviorally covered merely
because `--help` succeeds. Live reads and bounded cache initialization may be
added as redacted evidence outside the repository. Telegram mutations remain
pending explicit authorization; their offline fakes/TL invokers must be kept
separate from live claims.

The phase ledger in `docs/next-release-plan.md` is the source for release gates;
this matrix is the command-level audit trail and should be regenerated or
updated whenever Cobra commands change.
