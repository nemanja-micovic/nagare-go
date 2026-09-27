# Record the README demo in a real terminal with VHS (https://github.com/charmbracelet/vhs):
#
#   go build -o nagare-go . && vhs docs/demo.tape
#
# scripts/demo-gif/make.sh produces the same recording without VHS.

Output images/demo.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 780
Set Theme "TokyoNight"
Set TypingSpeed 60ms

Hide
Type "./nagare-go demo --speed 1.5" Enter
Sleep 1.5s
Show

# The overview: four agents across three repositories.
Sleep 3s

# The palette finds anything; open the agent about to ask for permission.
Ctrl+K
Sleep 400ms
Type "token"
Sleep 800ms
Enter
Sleep 3s

# Answer its permission prompt in place.
Enter
Sleep 3s

# Split: more agents, all live.
Alt+V
Sleep 2.5s
Alt+V
Sleep 3s

# Review what the first agent changed.
Alt+Left
Alt+Left
Sleep 400ms
Alt+D
Sleep 2.2s
Down
Sleep 1.8s
Escape
Sleep 1s
