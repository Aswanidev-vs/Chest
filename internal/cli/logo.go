package cli

// ChestAsciiLogo faithfully replicates the open Minecraft chest with floating particles and CHEST typography using pure ASCII and whitespace.
const ChestAsciiLogo = `
                   [#]
                       [#]
                 [#]     [#]

           +-------------------+
          /                   /|
         /                   / |
        +-------------------+  |
        |   \           /   |  |
        |    \  [===]  /    |  |
        |     \_______/     |  |
        +-------------------+  |
        |                   |  /
        |                   | /
        +-------------------+/

        ____  _   _  _____  ____  _____ 
       / ___|| | | || ____|/ ___||_   _|
      | |    | |_| ||  _|  \___ \  | |  
      | |___ |  _  || |___  ___) | | |  
       \____||_| |_||_____||____/  |_|  

             ORGANIZE YOUR WORLD
`

// RenderChestLogo returns pure ASCII chest logo
func RenderChestLogo() string {
	return ChestAsciiLogo
}
