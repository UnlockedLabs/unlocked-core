# Changelog

All notable changes to this project will be documented in this file.

## [1.6.0](https://github.com/UnlockedLabs/unlocked-core/compare/v1.5.0...v1.6.0) (2026-10-26)


### Features

* EN-196 move open content jobs out of provider middleware ([0ad7e80](https://github.com/UnlockedLabs/unlocked-core/commit/0ad7e8018ac276bd8b665e7747b422c5a93266af))

## [1.5.0](https://github.com/UnlockedLabs/unlocked-core/compare/v1.4.0...v1.5.0) (2026-10-22)


### Features

* En 177 stop startup migrations ([#1258](https://github.com/UnlockedLabs/unlocked-core/issues/1258)) ([ee53c01](https://github.com/UnlockedLabs/unlocked-core/commit/ee53c017188d6ef4e27caa4b284de04513892e1f))

## [1.4.0](https://github.com/UnlockedLabs/unlocked-core/compare/v1.3.0...v1.4.0) (2026-10-22)


### Features

* En 216 track learning record pdf exports main ([#1260](https://github.com/UnlockedLabs/unlocked-core/issues/1260)) ([8a87afb](https://github.com/UnlockedLabs/unlocked-core/commit/8a87afb2f4a5f4ddf1f971b9191c482b9a08a728))
* move Provider Platforms into its own module (EN-193) ([7c9b271](https://github.com/UnlockedLabs/unlocked-core/commit/7c9b271d7373fb4ab7ca90ae5bf6d30a94ec30f7))

## [1.3.0](https://github.com/UnlockedLabs/unlocked-core/compare/v1.2.0...v1.3.0) (2026-10-20)


### Features

* **backend:** run background jobs in their own process (EN-191) ([fc08238](https://github.com/UnlockedLabs/unlocked-core/commit/fc082380025642fca5a5adf481a468fad6fc604f))

## [1.2.0](https://github.com/UnlockedLabs/unlocked-core/compare/v1.1.0...v1.2.0) (2026-10-17)


### Features

* **backend:** add /api/version endpoint (EN-185) ([#1255](https://github.com/UnlockedLabs/unlocked-core/issues/1255)) ([4186f5d](https://github.com/UnlockedLabs/unlocked-core/commit/4186f5dfc908dc415164d9ffdc987246855ade85))

## [1.1.0](https://github.com/UnlockedLabs/unlocked-core/compare/v1.0.0...v1.1.0) (2026-10-16)


### Features

* **backend:** add feature module folders and boundary checks (EN-190) ([889be86](https://github.com/UnlockedLabs/unlocked-core/commit/889be86821ecbb8d972ce3737a39b5a46180e09e))
* **backend:** add internal feature-check endpoint (EN-189) ([#1253](https://github.com/UnlockedLabs/unlocked-core/issues/1253)) ([b1cad5d](https://github.com/UnlockedLabs/unlocked-core/commit/b1cad5d87bd81e90722f178a301ba6b45bd51419))

## 1.0.0 (2026-10-15)


### Continuous Integration

* add release-please for automatic versions (EN-181) ([#1251](https://github.com/UnlockedLabs/unlocked-core/issues/1251)) ([362d4ed](https://github.com/UnlockedLabs/unlocked-core/commit/362d4ed0f76e679b05f900506363f74c5572b446))

## [0.0.7] - 2025-05-14

### 🚀 Features

- Add program overview dashboard with test data (#784)
- Add proper tab separation change trending content to home (#787)
- Add logic to determine docker image version to build
- Add logic for displaying resident profile overview per #714
- Add logic for displaying resident profile overview per #714
- Add logic for displaying resident profile overview per #714
- Add audit trail
- Add resident Help Center and FAQs for UnlockEd direct support per #17 (#791)
- Add visibility by facility for videos (#790)
- Modify help link management so helpful links are managed and defined at facility level
- Add welcome tour
- Add logging for search terms and categories selected by user
- Add sql migrations for program features development
- Add full page create program form
- Add functionality to create and schedule new section for facility admin
- Add search suggestions
- Create class enrollment functionality for facility admins
- Add section status modification and archiving (#810)
- Add doc number to profile overview and search
- Add functionality so facility admin can add events to the class calendar per ticket 93
- Add list view to knowledge center components (#816)
- Add functionality so facility admin can edit an existing section per ticket 48
- Add functionality so facility admin can edit an existing section per ticket 48
- Add functionality for transferring resident between facilities for dept admin per ticket 100
- Add functionality for transferring resident between facilities for dept admin per ticket 100
- Remove and comment out history action logic
- Update resident profile and add account history
- Enrollment details page, program completions and assorted fixes
- Return redirect to proper 404 page if route invalid
- Add attendance tracking to program class events
- Add user programs to resident profile
- Add functionality for viewing class overview page per asana ticket 153
- Add functionality for viewing class overview page per asana ticket 153
- Add cron tasks for inserting prog history daily
- Add api endpoint for program overview dashboard
- Add prog management real time data, fix backend queries
- Add date range to prog management
- Adds archived programs filter
- Add facility admin logic to queries and frontend for prog management
- Update middleware to be offline first + assume s3 bucket is mounted
- Add script to add kiwix libraries to dedicated instance
- Update program overview with program details and history per ticket 165
- Create resident overview of programs

### 🐛 Bug Fixes

- Requested changes
- Correct order by and for library page and fix go test
- Requested changes
- Engagement graph fixes for op insights
- Ui fixes for layout
- Use user created at for joined in resident profile, clean ups
- Use constant value for session length
- Ui fix for grid layout
- Header typo
- New user pass lowercase fix floating text add header helpful links (#788)
- Change order by of libraries within the knowledge center to be by title ascending per asana task 40
- Add order by to be created_at when order_by parameter does not exist
- Rename duplicate migration file
- Display proper form on initial password reset, add logs
- Add method to retrieve all english language libraries to avoid kiwix mutiple language error (#797)
- Add css for fixing clipped text on the trending content when screen size is small
- Styles for icons
- Random bugs fix add back admin pass reset (#800)
- Remove duplicate navbar entry
- Modify structs and seeder to conform to program database structure (#803)
- Removed horizontal and vertical scrollbars (#804)
- Fix resident home sidebar helpful link navigation
- Password reset permissions and video thumbnail issues
- Update bug fixes on tour
- Remove doc_id for admins (#822)
- Remove user_history references from migration
- Updates success text and error rendering on program overview dashboard (#824)
- Support searching by doc for unenrolled users
- Fix history navigation (#815)
- Filter program enrollments by facility (#828)
- Modify dynamic page title logic
- Remove comments and modify path for a class edit
- Remove comments and modify path for a class edit
- Change class_status to status
- Add null check on end_dt
- Modify to use loader for loading class data
- Remove change per request
- Add LOWER function and ToLower to docID
- Change validResident variable name to transferNotes
- Add function to run conditional check
- Remove fixed width from button
- Create new component for transfer summary for confirmation modal
- Modify to use route loader to load facilities
- Remove success toast message
- Labels for transfer summary
- Replace context logic per preston request if not using querycontext for anything
- Update manage account with buttons for delete and transfer
- Fix order for facility id, transfer create account history to handler
- Update styling and api call
- Enforce user acct history permissions and hardcode query filtering
- Updates to landing page (#823)
- Fix full name search when enrolling users
- Use pg_trgm for fuzzy matching and make it consistent across db methods
- Return tx from fuzzysearchUsers to appease the linter
- Remove unused imports
- Document route loader and fix unenrolled user query
- Fix void that caused issue with sending event attendance to back end
- Implement sql for attendance GET gray out button
- Fix mutate on notes and search reset
- Add doc id column
- Fix toggled row after search
- Add datepicker change time period of events being returned
- Udpdate migration to include psql extension
- Switch database attendance call to batch
- Fix more change requests
- Assorted fixes. make routes consistent, cleanups
- Remove drop extension from migration
- Added logic that throws errors when adding a username with invalid chars
- Requested changes
- Fix display format of user names
- Modify labels on create class form (#842)
- Move other to bottom of enum (#838)
- Block out status sections
- Add finalized status groupings
- Re-calculate attendance status after recent merge
- Fix NaN and conditional logic
- Ui styles for table and handlenavigate
- Fix navigation route move business logic
- Error message in resident programs page
- Used default style for cancel
- Fix archiving text cancel button color
- Fix added brace in route
- Extract multi select code into its own component per ticket 96 (#845)
- Change custom card component to use StatsCard component
- Modify statscard to fix clipping of tooltip text and make cursor pointer display over status dropdown
- Log admin audit after handler call to use in-handler log additions
- Connected resident enrolled data and overall completion
- Remove commented code, resolve imports in modal components
- Generalize time filter switchers, remove facility dropdown on program management
- Correct dropdown select
- Insert daily prog slices allocated memory
- Map program types to correct column for prog mgmt dashboard
- Fixes completion rate showing 0 by returning percentage directly
- Map active enrollments and facilities to correct column for prog mgmt dashboard
- Correctly calculate totals per program
- Correctly calculate totals per program
- Updates migration tables and structs for attendance marked
- Add pagination and remove unused types
- Handle for no stats in cards
- Adds tooltips, fixes aligning
- Use debounce for search
- Remove useeffect from prog mgmt
- Add indexes to prog mgmt migrations, add default date
- Update time filter to days, simplify query for programs
- Add with context to user db calls for login metrics
- Operational insights frontend fixes, update migration file name correctly
- Use inner joins for facility programs, add pointer to reuse value in middleware
- Remove aggregate functions and move to group by clause
- Operational insights resident filtering done correctly
- Fix knowledge insights filter for all time
- Add with context to knowledge insights db call
- Get correct total residents and admins
- Update routes, cut off tags after 1 and add ...
- Condense category trimming and mapping
- Fixes filtering for facility admin prog managmeent page, fixes count
- Reformat to pull from programs and join on dpfh, reformat frontend
- For null values, create tooltip to display. fixes overhang of page
- Cleans up frontend code, allows for null values in backend
- Removes archived from active calculations, safeguards against empty string attendance
- Remove pcea when checking attendance status
- Operational insights filter by role = student
- Add logic check for class status of completed and cancelled per ticket 167
- Guarantee only system admins may edit or delete facility
- Change password logic for admin and resident specificity
- Add class overview tab functionality for attendance and enrollments per ticket 166
- Remove css padding and condense divs into one
- Modify query to pull only residents with status of Enrolled per ticket 187
- Replace ProviderAccess feature with ProgramAccess feature for attendance
- Replace spelling mistake with correctly spelled variable name
- Fix dropdown display names for timezones
- Remove displayNames in index.ts
- Remove class status Pending per ticket 134
- Remove enrollment status Pending per ticket 134
- Remove duplicate migration file number due to recent merge and add new migration file
- Array length of program status types in seeder
- Made requested changes to logic which removed unique and specfic knowledge from generic FormModal components
- Made requested changes and added default value to the submit button
- Remove JSX import
- Fix hyphenated name validation
- Remove namecaracters validation update alphanumspace
- Keep navbar pinned position across client route changes
- Escape string properly in kiwix zim dl script
- Fixes github actions to pull correct history and check changes
- Takes away pull_request filter on github action
- Add redirect route for when route is program-classes/:id per ticket 189
- Modify logging statement
- Use proper git commands to find diff from PR to main in CI
- Add css to fix transfer resident button shift on click per ticket id 160
- Modify comment
- Clamp text on program overview dashboard
- Fixed-table size for class list
- Cancel enrollments upon cancelled class
- Move cancel updates to batch process
- Remove unused function
- Update db to transaction for proper rollback capability
- Update to singular transaction for updating program and enrollments
- Update enrollment method cancelled check
- Check for existing user when loggging in to prevent ident mismatch
- Change error text, update error page capability
- Update error text
- Fix eslint option confusion on errortype
- Remove auth-required
- Remove variable assignment to fix linting error
- Remove duplicate migration file number due to recent merge and add new migration file
- Reduced size of program stat cards
- Move method functions to proper locations
- Change logging statement
- Modify dropdowns to be consistent for sorting resident names per ticket 139
- Modify universal OrderClause method to add sort by name_first, ascending,  when user is sorting by name_last
- Remove name_first from universal OrderClause method moved it to within the specific query calls
- Create shared method to add name_first to order by clause
- Remove the use of btn class from web pages and either use custom button class or create new class per ticket 37
- Modify styles for dark mode and change input to button
- Modify cancel button to consistent with submit button
- Remove unused/unreferenced file
- Change button-red to use text-white
- Condense button styles
- Remove label attribute from dropdown
- Modify shades of red for light and dark mode
- Modify resident button and text colors
- Modify universal OrderClause method to add sort by name_first, ascending,  when user is sorting by name_last
- Nil map check in scheduler prevent crash on general tasks

### 🚜 Refactor

- Remove info logs to make websocket logging less verbose in prod
- Add request context to all db calls using queryCtx
- Remove axios in favor of fetch api
- Rewrite seeder
- Accounted for the case when no one is currently enrolled and everyone has completed the program in the GetProgramClassEnrollmnetAndCompletions
- Moved program enrollment metrics to the dataLoader
- Added onChange to VerifyResidentModal doc_id text input
- Added isDisabled functionality to the doc_id input of the VerifyResidentModal
- Alter itermap fn to allow returning diff type than operating on
- Remove database call from loop in create facility handler

### ⚙️ Miscellaneous Tasks

- Update ytdlp version
- Update action for migration checking
- Switch section to class (#821)
- Bump middleware dependency
- Bump depdendencies
- Finally fix the CI builds
- Change log field to test CI
- Yeet test that fails now that the function works properly

## [0.0.6-beta] - 2025-03-04

### 🚀 Features

- Add local kiwix dev environment
- Add dept admin, add facility admin
- Add helmfile and example deployment template info
- Refactor modals
- Add bookmarking
- Category tag dropdown to programs on admin page (#782)
- Modify library visibility to be per facility according to ticket #718 (#779)

### 🐛 Bug Fixes

- Libraries reorder when hidden/shown
- Video downloads from playlists duplicated external_ids
- Search capitalization issues
- Increase column length for content_url to prevent errors in activity
- Make video metadata sync synchronous to prevent overload child processes
- Make all video operations syncronous
- Resize youtube png
- Fix order by add created at (#729)
- Rename getQueryArgs to getQueryContext, adjust for P99 latency
- Helpful link thumbnail update on change (#739)
- Fix helpful link thumbnail issue
- Use google cloud api key to fetch youtube metadata
- Login/auth issue for stale or invalid flow ids
- Handle auth refresh fail gracefully
- Use external nav for authcallback redirect in login form
- Dont mount shell script to pg docker container
- Correct admin to admin permissions
- Add facility_admin to role list in user query
- Remove admin ability to alter data in student view (#773)
- Dont mount shell script to pg docker container
- Have open content search return all appropriate results
- Clicking on item in library viewer search
- Ensure content_type is sent to search results, lose default auto focus in bar
- Remove search from trending content library card
- Hide ui controls when no videos present
- Add gh action + pre commit hook to detect duplicate migration files
- Use migration check action in image builder
- Duplicated routes from merge conflicts
- Show facilities in dropdown
- Remove email render for residents, remove prov platforms if 0, fix bug on prov platforms

### 🚜 Refactor

- Add query context to database cleanup handlers
- Clean up some code post merge conflicts
- Total activity time query 
- Change add student to add resident, change url length (#740)
- Create context to set title of page in authenticated layout directly 
- Replace string literals with constant for authcallback value in frontend
- Linting fixes
- Redo all client side routing

### ⚙️ Miscellaneous Tasks

- Rename fields to utils for server utilities
- Update yt-dlp version
- Switch sqlite driver for go tests

## [0.0.5-pilot] - 2025-02-03

### 🚀 Features

- Add library database schema and migration
- Implement queries into kolibri provider middleware - issue 392
- Add unique constraint on milestones for user_id, course_id, extern_id
- Add prometheus middleware and endpoint
- Add additional prometheus metrics
- Add libraries api and tests
- Change status of PR to "In Review" when added to project
- Updated to use table-2
- Updated ui to meet specifications
- Kiwix library scraping, remove canvas seeder, make all hot reloadable
- Add open content management ui
- Add library reverse proxy middleware and handler, update frontend
- Add dynamic path values without rerendering PageNav component
- Added headers to all pages with px-8
- Download lib thumbnails, add route loader, fix job scheduler
- Add facility context switch and ui
- Add toast context to reduce code duplication
- Add facility management
- Brightspace integration  (#473)
- Brightspace data model
- Add Programs page to UI and seed program tags (#471)
- Create-department-wide-program-464 (#477)
- Add GetUsers, IntoImportUsers in provider-middleware
- Add video download capabilities
- Add manual retry to video downloads
- Add logic for the integration of brightspace milestones (#483)
- Create new program & mark department-wide (#485)
- Add tz dropdown to initial change password form for sysadmin
- Create feature access level checkboxes (#501)
- Impl import activity for course for BrightspaceService
- Impl feature flags
- Remove course favorites, add program favs
- Track and store open content user acitivty
- Add library favorites (#519)
- Add internal link component, make static content card use it (#517)
- Open content activity dashboard
- Add limit param for resources
- Add featuring libraries
- Add system admin option to seed data for demo environment
- Add handshake process between Brightspace and Unlocked - Resident-to-Student Mapping #522
- Add external course start date and end date so that a resident can view on course list per #525
- Add layer 0 operational insights page w/ charts for user data
- Create helpful links open-content #548
- Add featured library on dash, fix cursor pointer hover bug
- Add cron scheduling for Brightspace
- Add in progress to user my progress page and remove activity chart
- Alter open content column migration
- Update open content provider variables
- Update library and video variables
- Update open content item and queries
- Add job to overwrite/update all video metadata in s3 weekly
- Add changes to handle new tables and procedures that were added for courses
- Add changes to handle new tables and procedures that were added for courses
- Add ability to favorite helpful links
- Adding admin layer 1 dashboard
- Add collapsable featured libraries
- Remove blocking on scheduler for provider with no courses
- Layer 1 desident dashboard, organize favorites by content type
- Add helpful links tab to student view, other bug fixes
- Add workflow to label issues with "Needs verification" when moved to Done
- Learning insights dashboard
- Remove sidebar, render popular courses
- Update add-needs-verification-label.yml (#662)
- Add squid proxy dockerfile and config
- Kiwix search implementation
- Add categories to knowledge center

### 🐛 Bug Fixes

- Removes calendar from student and admin portal frontend
- Cron tasks foreign key issue creating default cronjobs
- Cleanup git merge mistakes, broken tests
- Add omitted line back into code as requested in review
- Move model helpers to resource.go
- Added indexes to external_id and url
- Many many es-lints, enable build pre-commit to prevent ci failures
- Run all linters on push/pr, fix lints from latest changes
- Add error check per golangci-lint errors found
- Display provider oidc info properly
- Condense library handler to single index method
- Decrypt provider access key in middleware
- Add model hooks for access key encryption
- Unmapped provider user search
- Fixes toast alert colors
- Strict equality check for toast state
- Made requested changes to PR
- Adjusted tooltip name
- Increase course name with migration and alter kolibri input
- Dropdown links to user page changed to admin/student
- Changed text-alignment from left to right
- Added requested changes to detail and ul elements on the PageNav.tsx
- Use 64 bit int for total weekly activity to prevent overflow
- First collection shows in modify section
- Fixed some components
- All lints and type errors with new build
- Github action to use yarn to resolve deps
- Use larger integer types for activity data
- More frontend fixes and lints
- Lints with library component
- Use yarn to build in dockerfile
- Assorted fixes from migrating frontend build system
- All lints and type errors with new build
- Lints with library component
- Typescript lints
- Golangci lint use strings.equalfold for case insensitive comp
- Use proper id for open content provider in scheduler
- Adapt oidc login to new frontend route paradigm
- Frontend routing, toast, path variables, facility info
- Admin dashboard rendering issue
- Kolibri uuid issue, ensure accounts are properly created
- Consent form oidc login, kolibri course paths, navbar link
- Redirects in consent form and reset password auth
- Removed grade from ui
- Remove daisyUI navbar class to fix scrollbar
- Route loading loop, display open content providers
- Add exporting struct fields to temp struct
- Remove duplicate access key label
- Add order by to monthly activity and change left join to inner join on milestones
- Modify insert daily activity function to handle kolibri time delta
- Modify GetUserCourses to filter out other user's courses changing by left joins to inner joins
- Kick down goose end statement
- Update condition to check for active link deletion
- Add logic for total progress milestones to monitor for when teacher adds more lessons or quizes to class
- Change to check for course_type
- Add facility management link back to navbar (#478)
- Modify anonymous struct to correctly parse http response
- Resource mgmt detects when no changes happen
- Ensure video key updates and handle UI issues
- Prevent kiwix from downloading/uploading thumbnails if they exist
- Updating users wasn't working correctly and tooltips were clipped (#481)
- Add facility management link back to navbar
- Multiple imports from merge conflict broke build
- Add frontend validation to user creation form to allow spaces in names (#502)
- Auto enable videos when recv metadata from s3
- Pagination on admin and student mgmt pages
- Removed role dropdown on add user
- Removed old files
- Add csvs directory to root of project and remove subdirectory from provider-middleware
- Update union to include only having, text doesn't overlap flag
- Add time to date in seeder for start and end dates of courses and change stop to end
- Default to Kiwix thumbnail for small image libraries (#575)
- Changeed  dashboard -> Learning Path  for student
- Add tooltip to make users aware of disabled favorited content
- Dashboard label navbar logic
- Prevent scheduler from updating relation on task, lints
- Oauth issue with internal navigation being used for full TLD url
- Assorted hotfixes from demo review
- Assorted hotfixes from demo review
- Assorted hotfixes from demo review
- User courses improperly calculating time
- Seed demo and user catalog
- Seed demo and user catalog
- Alter table individually
- Correct visibility status query and rendering
- Remove sql line in  migration
- Helpful links before create name to title
- Change Raw to Exec, add update to total progress milestones when 0 to be 40 and add outcome when course is complete
- Stop navigation to library viewer when favoriting or unfavoriting from knowledge center
- Correct logic for descending date
- Spelling, revert course catalog changes, fix oauth callback
- Favoriting/featuring on trending content page
- Update library card component
- Set logging back to stdout to access logs in kubectl
- Update tabs and remove use effect
- Change Knowledge Center to Knowledge Base (#620)
- Change to use Activity UpdatedAt column
- Time output on my progress
- Add button class on  Admin and  Student page, and pencil icon fix (#633)
- Add titles for pages (#632)
- Hover-state icons, display of course titles, featuring/fav functionality
- Add dsn env var to standardize across services
- Add tooltips and line charts to operational insight (#630)
- S3 timeouts on uploads to sync metadata
- Add dotted box to indicate no featured content on knowledge insights
- Fix helpful links name persistence and accidental navigation on helpful links
- Move links on menu per #611 and modify logic for correctly dispaying menu links per #647
- Modals reset now upon close and reopen (#645)
- Linting
- Made requested changes
- Use aws region env var and prevent username changes
- Fix facility list item in page nav
- Standardize toaster messages (#635)
- Remove featured content box if empty and not admin (#659)
- Remove breadcrumbs, move page titles (#657)
- Handle for no activity or courses student layer 2 dash (#669)
- Added optional variable to pagination component
- Fix navbar issues when screen is smaller
- Modify css and add conditional for left-aligning page titles with content on page per #663 (#689)
- Make entire video card clickable and enhance course card display (#688)
- Modify logic to disable favorited content items rather than hide them (#691)
- Issue template CI file
- Add missed merge line of javascript
- Modify admin and resident side nav, helpful link card consistency, and unfavoriting a helpful link per issues #658, #678, and #692 (#693)
- Nav labels, card styles, and text clamping
- Wrong col count for helpful links (#699)
- Add existing helpful link toast (#698)
- Add a line of javacript to fix issue with favoriting a video per issue 703
- Add NATS cache to AdminLayer2 (#695)
- Alter table by changing primary key to be id and add unique contraint

### 💼 Other

- Fixed build process

### 🚜 Refactor

- Removed remember me checkbox from login page
- Applied requested changes from PR#388
- Added 6th param to each call to db.GetCurrentUsers() to account for adding user role to the signature when refactoring form dual user mgt pages
- Fix more lints
- Start building ULIComponent
- Completed heroIcon component replacement on the resourcesManagement page
- Completed heroIcon component replacement on the studentManagement and AdminManagement  pages
- Completed heroIcon component replacement on the ProviderCard.tsx
- Completed heroIcon component replacement on the PageNav.tsx
- Removed nested components from the ResourceManagement.tsx (#500)
- Merge main, clean up open content frontend, add routes
- Changed open-content to knowledge-center
- Remove 'Program Attendance' table from MyProgress page (#549)
- Update navigation and routes for layered access
- Modified logging statement to include library name
- Adjusted sorting options for FilterLibrariesAdmin
- Used common enum FilterLibrariesAdmin for VideoManagement dropdown filer
- Adjusted sorting options for helpful-links
- Remove unused components, interfaces, models and apis

### 📚 Documentation

- Add changelog post refactor + git tag

### 🎨 Styling

- Tightened up trending content for smaller screens (#690)

### ⚙️ Miscellaneous Tasks

- Update yt-dlp (#520)
- Update dockerfile + provider dependency
- Go mod + go sum updates/fixes
- Update aws packages to avoid s3 credential caching issue
- Updated pr-template (#668)
- Update video dependency
- Add pull request labeler action

## [0.0.2] - 2024-09-25

### 🚀 Features

- First remote
- Add github action for conventional commits
- Add github action for Go linting
- Add fontend app
- Add basic skeleton, begin to draft things
- Add first basic user handler
- Progress on users table + handlers
- /api/login endpoint, JWT working
- Authentication works, routes are protected
- Add useractivity middleware, include provider services middleware
- Add provider services, impl provider_user_mappings
- Make create user return temporary password
- Move directory structure around
- Add action to import users from kolibri with middleware
- Add git hook for format + linting pre-commit
- Add tests for user endpoint and seeders
- Run tests in github actions CI
- Dockerize app
- Add docker compose file, fixes
- Add logging methods
- Generalize provider service middleware to work with ID's
- Finish canvas import users
- Add programs, milestones, provider tests
- Fix dockerization
- Fix service to adjust to programs
- Add v2 frontend from other codebase
- Finish initial port of frontend
- More UI work porting over
- Ui fixes, continue port
- Add initial outcomes, fix foreign key issues
- Add activity
- Add activity to replace UserCourseActivity
- Add hydra to docker compose
- Add hydra and fix database tables
- Complete redesign of dashboard, new fonts, new colors
- Work on oidc client registration
- Setup static file serving in docker, prepare for deployments
- Oidc flow with hydra
- Oidc route, work on redirects, hydra
- Adds cards and card list to my courses page, random fixes on dashboard
- My progress page updated with mock data
- Add frontend/UI for auth client registration of providers
- Canvas middleware activities + milestones
- Init go workspace, move shared src code to root directory
- Add logrus structured logging, fix lints
- Get milestones for program user in middleware
- Module exports in middleware, register routes
- Adds course catalog page, updates course pills
- Changes format of course catalog list card
- Add canvas-seeder
- Add cron scheduling
- Add cron scheduling service
- Import programs from canvas
- User dashboard endpoint
- Rearrage codebase, remove jobs
- Fix user dashboard sql query for output
- Serve frontend in nginx container
- Prepare build script and compose files for many environments
- Move provider middleware into scratch container, in-memory sqlite db
- Add seeder, courseCatalogue endpoint, user favorites
- Dashboard endpoint and fix seeder
- Finish dashboard + catalogue issues
- Uses dashboard api and updates some variable names to match import
- Add Puppeteer student discussion posts to Canvas
- Add ory keto, refactoring, oidc work
- Add kratos identity mgmt to docker setup, endpoint for ui
- Organize config files and docker configuration
- Ory configuration mgmt
- More ory kratos configuration work
- Initial draft of our UI login to kratos, identity creation
- Finish ory auth + login with our UI
- Adds daily user activity api and integrates with frontend
- Updates entire my progress page and relevant backend requests, updates seeder for outcomes
- Adds description, modifies activity for dashboard, updates seeder for realistic words
- Adds outcome pills to catalog, filters dashboard, updates time
- Ui for provider user mangement
- Backend impl for provider user management
- Clean up middleware, remove bloat
- Make backend aware of client ory sess, fix auth route and cache sessions
- Work on user management UI, auth fixes
- Prov user mgmt, fix session/auth issues
- Implement modal to view imported provider users
- Adds facilities table & admin dashboard backend
- Implement filtering users by facility_id, and admin context switching
- Add tests for user endpoints and facility contexts
- Add gh action to build + push containers to ecr
- Add gh action to build + push containers to ecr
- Adds outline of admin dashboard, separates admin/student dash
- Adds facilities table & admin dashboard backend
- Adds admin dashboard UI & connects to backend
- Adds password ui validation
- Backend impl user search
- Add fetching singular oidc client info for ui
- Adds resources to right side bar
- Adds wikipedia card to open content
- *(266)* Create a general component for search
- *(266)* Create a general component for search
- Backend impl user search
- Add fetching singular oidc client info for ui
- Adds authorization info to revisit
- Backend impl user search
- Adds activity graph on student dash
- Add demo ci/cd github action
- *(267)* Add search and sort to My Courses
- Add open content, fix outstanding issues with courses
- Add db function to create outcome for progress milestones
- Catalogue search, search fixes, makefile
- Adds confirm bulk import modal, fixes modal types
- Add facility name form to init psw reset
- Finish openid connect implementation
- Prevent kratos from prompting user again for oauth2 login
- Kolibri sql integration and dockerization
- Kolibri integration work
- Prep kolibri for oidc. auto create users in sync'd instance
- Finish kolibri OIDC implementation
- Add rev proxy routing to other oauth2 routes
- User-activity page removal (#357)
- Add unauthorized page and protect routes
- *(268)* Add sort to MyProgress.tsx
- Add nats to base docker compose setup
- Add cron-tasks binary to publish jobs + refactor middleware to sub to MQ
- Add adjustible cron schedule env var and fix scheduling
- Add provider user search
- Switch to goose from gorm automigrate
- Add air and frontend container for hot reloading in docker
- Add collapsible navigation component
- Add unauthorized page and protect routes
- Updated course catalog pills w resp. design
- Impl api class to frontend to simplify components
- Simplify backend responses, remove in-house auth, fixes
- Add custom error type with method to handle error type
- Initial commit of custom error type with method to handle error type
- Initial commit of log fields struct wrapper with internal logging methods to use with HandleError method
- Add reflection to get handler name and change LogFields type to sLog type
- *(334)* Backend validation for frontend forms
- Add more fields to user validation and fix client err display
- Work in progress
- Merge preston main branch, implement tests for backend api, correct bug issues
- Cleanup code
- Removed unused files
- Removed unused files and uncomment out HTTP test scenario code
- New resources management ux
- Add custom error type with method to handle error type
- Add healthcheck endpoint, toggle provider active

### 🐛 Bug Fixes

- Move logs to proper directory, add .gitignore files
- Lint
- Create logfile if not found
- Remove commmited cookie-jar
- Error handling for lint
- More git hook errors
- Ci tests
- Complete dockerizing service/middleware
- Correct issue where server starts before PG
- Login issue where cookie wasn't being set properly
- Move .env.example to root directory
- Correct filepath to frontend public folder to host imgs
- Tests and lints
- Ui linting + formatting
- Remove build dir from git
- Login error with redirects
- Remove additional authcontext
- Lsp import errors from @/* imports
- Welcome page login issue
- Errors with fetching using SWR
- App env for development
- Cors issue
- Another cors issue
- Auth flow
- Correct login form error behavior keep it from throwing
- Remaining slog calls
- Remove duplicate table list for migrations
- Logging dependencies in middleware
- Logging in backend
- Changes fallback background to correct colors, fixes large pill on card view of course catalog
- Fixes catalog list card background and spacing
- .env.example for canvas-seeder
- Add separate .env.example for canvas-seeder
- Scheduling deadlock
- Correct programs importing from canvas
- Correct programs importing from canvas
- Lint error ci
- Tls error for deployment
- Oidc informatiion returned to the client
- Tls issue in scratch container
- Dashboard sql
- Add missing user dashboard field, dont return resource
- Remove debugging delay and non-headless mode
- If recent courses is empty, still keeps the correct sizing of cards
- Remove favorites field from milestones
- Add return after nil check for user pointer
- Add proxy and redirect if no login flow is started
- Fixes login issues, adds week activity to dashboard api
- Removes unused code on dashboard api
- Fixes pulled time back to seconds, removes extra lines of code
- Adds id to catalog return
- Removes soft deletes from user favorites
- Removes unused variables
- Fixes lint errors on latest push
- Adds files for build dev to work
- Activity errors, ci issues
- Docker compose file duplicate entry
- Actions caching and deduping users
- Auth issue where running in containers
- Work on middleware fixes
- Correct user importation behavior from providers
- Add warning to reset server if migrate fresh
- Logout issue with kratos redirect
- User activity page link, add tests, random fixes from PR
- Test action to build container and push
- Conditional builds for gh action
- Correct github action behavior
- Finally fix the github action
- Conditional builds for gh action
- Diff algo to determine cond. build
- Diff refs/remotes/main to fix conditional output
- Add some logging and retry github actions for ecr push
- This github action better work this time so help me god
- Github action test, will have to rebase + squash these commits
- For sure squashing these commits
- Finally fix the github action
- Finally fix the github action
- Kubectl config for staging deployments
- Condenses convertSeconds & rounds on admin dash
- Add ecr secret creation to k8s workflow, prettier formatting
- *(299)* Fix search, remove chevrons in activity and users pages
- Merges main to resolve merge conflicts
- Actually merges in branch :)
- Updates pills to take in text
- Add fields to oidc client info
- Fixes grid color in dark mode
- Remove open content unused import
- Kolibri username OOB error
- Corrects recent activities course_progress on student dash
- Kolibri user importation
- Remove yarnrc and other bloat
- Providerusermgmt keep users selected when page turns
- Prevent importing duplicate users providerusermgmt
- Is_public cast to bool when canvas can return nil
- Changes activity col
- Updates left menu to resources
- Homepage recent courses cards UI fixed
- Adds inner background css
- Colors on login with theme
- Adds provider platform name on prov user management
- Updates top milestones graph on admin dash
- Fixes stats card spacing & card overflow
- Adds content on no enrollments to student dash
- Student dash enrolled courses truncates alt name
- Filter by facility id when getting top programs for dash
- Empty recent programs for user-dashboard
- Small UI fixes for staging
- Returns overflow auto to cards & implementes overflow hidden on others
- Removes alt names from admin dash charts
- Fixes grid cols on student dash:
- Hardcodes facility name
- Fixes pagination not working on later pages on Users page
- Externally links to courses in list view
- Stop from refreshing page when incorrect psw
- Adds real kolibri link to resources
- Adds wiki deployment
- Fixes top courses final box on admin dash
- Adds warning message on reset password
- Resources page id's for proper deletion
- Order by even when not sorting catalogue
- Ensures all modal buttons are submit not submit query
- Fixes import users modal on dark mode
- Adjust npx husky command
- Fixes all linting warnings
- Submit button value
- Replace redirect to kratos when no flow is found in /login
- Validation error when creating preexisting username
- Fixes entire page re-render for course catalog, adds error rendering
- Remove unused import to fix container build
- Fixes mapping modal, updates pagination, updates seeder
- Changes seeder back
- Switch to justfiy-between for my progress css class
- Golangci-lint version to work with go 1.23
- Adjust provider readme for queue impl and fix env
- Return consistent kv pairs of users/programs in job params
- Correct incorrect check for nil error on a db.tx value
- Link to kolibri oidcauthenticate to oauth user automatically
- Unused imports
- Unused imports
- Frontend types, lints and seeder issue + update makefile
- Issue with consent page/oauth, seeder, retain build info
- Remove unused database methods
- Change else if's to switch
- Modify comment
- Correct activity page for failing ci despite unused
- Dockerfile to match new main.go location
- Attempt to migrate to new kratos schema in staging
- Force pass reset if traits are missing in kratos to recreate
- Correct login hanging on invalid credentials, refactor API class
- Fresh migrations nats authentication
- UseSWR hooks data undefined + optimize sql query, add env example
- Prevent kolibri from being returned to UI for user creation in pp
- Make login refresh trigger by changing return type of API call
- Code review modifications
- Frontend types, lints and seeder issue + update makefile
- Change docker-compose to docker compose in makefile
- Disallow kolibri user management
- Remove unused schedule field from task struct
- Tasks foreign key mixup
- Add provider id to runnable task insert in scheduler

### 🚜 Refactor

- Runs prettier on previously committed frontend files
- Move initLogging out of main, set log level
- Formatting and prettier
- Pull out error handling from server response
- Provider plaforms UI updated, pill change WIP
- Reduces code
- Prettier config file to prevent overlapping changes
- Removed commented code
- Replace anys with correct types
- Make server methods + handlers priv access
- Refactored resource cards & types

### 📚 Documentation

- Update readme to reflect build
- Add first changelog
- Add instuction for seeder in readme
- Add common issues to readme, fix provider middleware readme
- Add readme info about kolibri dockerization and development

### 🎨 Styling

- Dropped new facility name below form input and trimmed whitespace upon submit

### 🧪 Testing

- Temporarily rm backend tests till refactor is over

### ⚙️ Miscellaneous Tasks

- Fix working directory of go lint
- Merge folder canvas-discussion-bot into canvas-seeder
- Remove canvas-discussion-bot
- Adds prettier formatting file and formats all frontend files
- Add pull request template

<!-- generated by git-cliff -->
