# Chat Header + Tool Drawer
[ ] The tool drawer works pretty well but I'd like to change it little bit. When closed, we should see a small tab that looks like it's "under" but sticking out of the chat header. The tab would appear to be slightly in the chat area. The tool icon can be on this tab. Let's center align the tab for now. When the user clicks on it, let's expand it like we do now. Right now it looks like a white box witha border between it and the chat, let's give it some style so that it looks more like a drawer. It should be about 80% width instead of full. It should sit one layer above chat and allow chat to slide under it when scrolling. They should be able to pull the tab to slide the drawer in/out or double click it to open/close. Let's get rid of the 2 layers of clicking. If they double click, it opens to the first preview size and any other size changes would be done by pulling the tab up/down. Long term, we may have other tabs/drawers up here for special things, just to keep that in mind. They will look and work the same way, just have a different tab and content. 
[ ] The current agents in session browser/modal, it only shows an avatar, the source, and version. We need to update that to be more mini-card styled and look similar to our agent cards in settings. Let's also make the avatars match our square with rounded corners style. For this view, let's not put a background or borders on the cards but let's make the content match our card layout. In between each agent we can have a thin, faded divider (shadcn component). 

# Chat composer
[ ] The border on dark looks good, on light it looks like the bottom bar is 1px narrower on each side than the top. Let's give it a proper border in light mode to match dark mode better.
[ ] The user icon is dead right now. Let's add a user settings/prefs/profile menu. Follow the same style as the main settings, but this will be user only stuff. Let's let them set their chat avatar, name, and manage the context *about* themselves that they want an agent to be aware of like job, birthday, interests, personal links, basic bio, etc. We should move keyboard shortcut here, and light/dark default preferences here (System|Light|Dark) and we'll put any personalization preferences here as well. 
[ ] The modals are largely too small, they need to be a bit taller and a bit

# Widgets Rail
[ ] The Session Info widget never seems to show anything, even when set. Let's make that one work and make sure it shows the current project too. 
[ ] Let's have a pencil icon in the top right corner of widgets that let's users drag/drop to organize the widgets however they want and let them click on the "eye" icon on each to show/hide it. 
[ ] The bookmarks widget, when a bookmark is set, let's kick off an autotitle action for the bookmark. Right now they are all set to bookmark. The auto title should be just for the bookmarked text/block.

# CRUD ops in settings, etc.
[ ] All should use a modal vs inline. Ideally users always stay on the screen they are on. When forms are submitted in modals, the parent page should update via SSE, no polling/refreshing.

# Agents
[ ] On the agent directory, let's make the tags use colors like we do on prompts
[ ] MCP Servers, Tool Permissions are both just json blobs, let's make them proper fields. I suggest the repeater pattern.
[ ] Tags should have auto-complete. I suggest using TipTap for this (And generally, if we need stuff like that, TipTap may be handy). ~/Projects-apps/nanite has a good example of this on the create/edit notes/todos screens. 
[ ] Tools and Directories also need to have better UI/UX forms
[ ] Default model should default to the default set in settings
[ ] I don't like the seed as a source. Let's change that to system or core? 
[ ] For prompts, let's call them prompts intead of templates
[ ] On the detailed agent view, I think we should remove mode, mode is more harness related (planning, execute, etc) and we reflect that but not set it. I like the inline editing abilities on this page so let's consider improving the display of this page and letting all editing be inline like this as long as it looks a bit better. We can use modals to add things like more directories, tools, etc. and have the profile update via SSE. I think just click on anything to edit it (like title, descripton, etc). Let's have an avatar picker vs. raw input and let's let them upload an svg or png as well. Make the avatar a rounded box with border-radius-sm for the corners like we are doing in other places. Let's make sure we have all the fields exposed that a user might need to see or edit. We should show what project an agent belongs to or allow them to add it to a project. Consider: Should agents be allowed to be on more than one project and I think the answer is yes, so many-to-many relationship between agents/projects
[ ] The "create" view should just be the same as the detailed view with any auto generated fields set. We should show the minimum required fields.

# Skills, Prompts, Tools, Plugins & Widgets
[ ] Make the "add new" button (if present) match the one on Agents. Most are the accent red color but let's make them the neutral gray we use on Agents. 
[ ] Let's make any tags use teh colors like we are adding to agents and that are currently on prompts.
[ ] Let's add an "icon" to all our object specs so that we can specify specifici icons for a skill, prompt, tool, plugin, widget. And let's expose that in our plugin system so users can set their own too. That way they can be specific to each one if we want. Let's use our current ones as defaults for now.

# Tools/Servers
[ ] Make server/tool cards clickable. On server details, should show all the server meta + a directory of it's tools (card view). On the tool cards, same thing but the detail view should show the context that's delivered to the agent for this tool + all the meta, etc.

# Plugins
[ ] Card should click and go to plugin details. Plugin details should be all the meta with links to the author page/repo, descriptions, etc. Plugins optionally should be able to provide an about/readme/how-to for their plugins. 

# General
[ ] I like Shadcn's "empty" component, we should use that and follow that pattern so nothing looks broken/unfinished. 
[ ] Make sure we are using Shadcn components everywhere. Pills/Badges, Avatars, arrows/nav, search input, comboboxes, cards, inputs, scroll areas, toggle (like our bookmarks for example), switches, everything. Let's use the shadcn Kbd component anywhere we display keyboard shortcuts (eg: the shortcuts settings page)
[ ] Let's make sure things that might need them have tooltips.
[ ] The search needs to be created. Let's pop up a combobox for search in a modal. It should search chats for now. Let's have filters/sorts in the combobox so they can filter by Workspace, Project. By default it should scope to the current Workspace/Project. Users should be able to use keyboard to navigate the results and when selecting one, hit enter to go to that chat session and specific chat message is focused. See escape behavior below, make sure it works properly here. Let's also create a search shortcut as shift shift by default. 
[ ] When I'm on say, edit an agent, if I click on a menu item it doesn't take me to that menu item. I always have to use the back button on whatever screen I'm on. No matter where a user is, if they click a menu/nav item it should do that thing's action (rare exceptions, none currently known).
[ ] The escape button is special in our system. If a modal is open, it should close it like we would expect. It should cancel things like normal in normal contexts. But if a user is on a page or screen and hits esc, it should go back to the previous page they were on. Not back from a fixed nav point of view, but literally back to the previous page. If they hit escape enough, it should take them all the way to the home/start page when the app launches. This means we need to track their pages/views enough to make this work.
[ ] Let's create a command pallet and map it to cmd+k for mac by default. It should have things like search, new chat, new workspace, manage workspaces, new project, manage agents, skills, prompts, etc. Should be a quick way to get to most things. Let's organize it well by grouping related items. Consider: Should we have them drill down, like "Manage Agents" when they hit enter on that would show the sub-commands/routes for agent related stuff? Should have settings shortcuts too, let's use Shadcn Command as the base.
[ ] Make sure to add all keyboard shortcuts to the keyboard shortcuts preferences/settings screen. Make sure shortcuts support mac, windows, and linux.
[ ] Let's add right click / context menus where it makes sense
[ ] Let's use skeletons for loading anywhere it makes sense
[ ] Let's use optimisitic UI patterns

# Left Chat rail
[ ] Let's follow the Shadcn sidebar pattern for the Team Dropdown they have. We currently have chats with the + button in the top row of the left chat rail. Then the projects. I think we should replace the chats + part with the shadcn Team dropdown for the project there. Workspace can stay where it is for now.
[ ] Make this "infinite scroll". Let's load the most recent (x) chats and then the user must scroll for more. 
[ ] Need a delete/archive chat action. Delete should confirm, archive should just hide the item. We can expose archived chats via search filters for now, otherwise they are simply hidden. Let's use the context menu pattern for delete/archive. Let's also have add to project here so they can move chats to projects. 

# Main chat display
[ ] Let's use infinite scroll here too. When a user loads and old chat (say, using a bookmark or search) it should navigate to that chat message and load the previous 10 and next 10. Then, whatever direction the user scrolls it should add more in that direction. 

