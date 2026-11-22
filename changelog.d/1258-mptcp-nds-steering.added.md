- **Guest traffic now enters the MPTCP bond only after the portal admits it.**
  The new fw4 chain matches the NDS trusted/authenticated marks, runs after
  NDS marking, excludes the router's own addresses at apply time, and installs
  the fwmark rule TPROXY needs; disable removes the chain, rule, and table
  ([#1258](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1258)).
