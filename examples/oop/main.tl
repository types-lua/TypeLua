--- TypeLua OOP without a `class` keyword: interface + metatable.
--- Types are erased; the runtime is plain Lua metatables.

interface Account {
    owner: string,
    balance: number,
    deposit(amount: number): number,
    withdraw(amount: number): boolean,
}

local AccountMT: map<string, any> = {}
AccountMT.__index = AccountMT

function AccountMT.deposit(self: Account, amount: number): number
    self.balance = self.balance + amount
    return self.balance
end

function AccountMT.withdraw(self: Account, amount: number): boolean
    if amount > self.balance then
        return false
    end
    self.balance = self.balance - amount
    return true
end

local function newAccount(owner: string, balance: number): Account
    local self: Account = {
        owner = owner,
        balance = balance,
        deposit = AccountMT.deposit,
        withdraw = AccountMT.withdraw,
    }
    return setmetatable(self, AccountMT) as Account
end

local acc: Account = newAccount("ada", 100)
print(acc.owner, acc.balance)
print(acc:deposit(50))
print(acc:withdraw(30))
print(acc.balance)
