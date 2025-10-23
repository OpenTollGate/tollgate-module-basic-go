
Terminal output:
```
c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go$ git checkout main files/etc/uci-defaults/99-tollgate-setup
Updated 1 path from caaac68
c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go$ git status
On branch feature/free-as-in-beer
Your branch is ahead of 'origin/feature/free-as-in-beer' by 1 commit.
  (use "git push" to publish your local commits)

Changes to be committed:
  (use "git restore --staged <file>..." to unstage)
        modified:   files/etc/uci-defaults/99-tollgate-setup

```

Ok, I restored @/files/etc/uci-defaults/99-tollgate-setup from main because things are still broken as you can see in @/free-as-in-beer-for-routers-owner/outcome-v5.md .

I expect @/files/etc/uci-defaults/99-tollgate-setup to make things work as expected if we run it after @/files/etc/uci-defaults/99-tollgate-setup . 

How can we turn @/free-as-in-beer-for-routers-owner/setup_private_ssid_v2.sh  into a uci-defaults script that runs after @/files/etc/uci-defaults/99-tollgate-setup ? 

